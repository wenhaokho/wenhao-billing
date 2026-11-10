package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth/oauthdb"
)

// spentCodeGrace is how long past its expiry a spent code's row is kept.
const spentCodeGrace = 5 * time.Minute

// tokenResponse is Python's _token_response body, keys in its order.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// value is a form field's last value; absent and empty are both "" (Python's
// `if not field`).
func value(form url.Values, k string) string {
	v, _ := last(form, k)
	return v
}

// token is POST /oauth/token (RFC 6749 §4.1.3 and §6) for public clients:
// there is no client authentication, only PKCE.
func (h handler) token(w http.ResponseWriter, r *http.Request) {
	form, ok := readForm(w, r, "grant_type")
	if !ok {
		return
	}
	now := h.clock.Now()
	// Spent codes past their expiry can never verify again, so their rows go;
	// the expires_at index keeps this cheap on every request. The grace
	// period covers clock skew between replicas: a row is only dropped once
	// every replica's clock has the code expired.
	if err := oauthdb.New(h.pool).PruneSpentCodes(r.Context(), now.Add(-spentCodeGrace)); err != nil {
		panic(err)
	}
	switch grant := value(form, "grant_type"); grant {
	case "authorization_code":
		h.codeGrant(w, r, form, now)
	case "refresh_token":
		h.refreshGrant(w, r, form, now)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type: "+grant)
	}
}

// validVerifier is RFC 7636 §4.1: 43 to 128 characters of [A-Za-z0-9-._~].
func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for i := range len(v) {
		c := v[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	return true
}

// pkceS256Matches is BASE64URL(SHA256(verifier)) == challenge, compared in
// constant time.
func pkceS256Matches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// codeGrant exchanges an authorization code, with Python's checks in its
// order. A refused exchange does not spend the code.
func (h handler) codeGrant(w http.ResponseWriter, r *http.Request, form url.Values, now time.Time) {
	code, verifier, clientID := value(form, "code"), value(form, "code_verifier"), value(form, "client_id")
	if code == "" || verifier == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "code and code_verifier are required")
		return
	}
	// Public client: client_id is required and must match the code, since
	// there is no client secret.
	if clientID == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}
	claims, spent, err := h.tokens.verifyCode(code, now)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid authorization code: "+err.Error())
		return
	}
	// An omitted redirect_uri never matches (Python compared None).
	if redirectURI, ok := last(form, "redirect_uri"); !ok || redirectURI != claims.RedirectURI {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if clientID != claims.ClientID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}
	// Go change 7: Python took any verifier and answered a non-ASCII one 500.
	if !validVerifier(verifier) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid code_verifier")
		return
	}
	if !pkceS256Matches(verifier, claims.CodeChallenge) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	// Go change 1: the code is spent and the pair stored in one transaction,
	// so of two exchanges of one code exactly one gets a pair.
	var resp *tokenResponse
	err = db.InTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		q := oauthdb.New(tx)
		_, err := q.SpendCode(r.Context(), oauthdb.SpendCodeParams{CodeHash: spent.id, ExpiresAt: spent.exp})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // already spent
		}
		if err != nil {
			return err
		}
		resp, err = h.issuePair(r, q, claims.UserID, now)
		return err
	})
	if err != nil {
		panic(err)
	}
	if resp == nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "authorization code already used")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// refreshOutcome is what a refresh transaction decided.
type refreshOutcome int

const (
	refreshRotated refreshOutcome = iota
	refreshUnknown
	refreshReused
)

// refreshGrant rotates a refresh token. client_id is ignored: refresh tokens
// are not bound to a client (kept from Python).
func (h handler) refreshGrant(w http.ResponseWriter, r *http.Request, form url.Values, now time.Time) {
	refresh := value(form, "refresh_token")
	if refresh == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	user, jti, err := h.tokens.VerifyRefresh(refresh, now)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token: "+err.Error())
		return
	}

	var (
		outcome refreshOutcome
		resp    *tokenResponse
	)
	err = db.InTx(r.Context(), h.pool, func(tx pgx.Tx) error {
		q := oauthdb.New(tx)
		// Go change 8: revoke under the row lock. Python read the row and
		// then updated it, so two concurrent refreshes could both rotate.
		_, err := q.RotateRefreshToken(r.Context(), jti)
		if errors.Is(err, pgx.ErrNoRows) {
			exists, err := q.RefreshTokenExists(r.Context(), jti)
			if err != nil {
				return err
			}
			if !exists {
				outcome = refreshUnknown
				return nil
			}
			// Reuse detection (RFC 9700 §4.14.2): a rotated-out token was
			// replayed, so every live refresh token of the user goes, any
			// in-flight successor included. Committed with the refusal.
			outcome = refreshReused
			return q.RevokeUserRefreshTokens(r.Context(), user.String())
		}
		if err != nil {
			return err
		}
		outcome = refreshRotated
		resp, err = h.issuePair(r, q, user, now)
		return err
	})
	if err != nil {
		panic(err)
	}
	switch outcome {
	case refreshUnknown:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
	case refreshReused:
		oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token reuse detected")
	case refreshRotated:
		writeJSON(w, http.StatusOK, resp)
	}
}

// issuePair mints an access and refresh token for user and stores the
// refresh token's jti (Python's _token_response).
func (h handler) issuePair(r *http.Request, q *oauthdb.Queries, user uuid.UUID, now time.Time) (*tokenResponse, error) {
	refresh, jti := h.tokens.MintRefresh(user, now)
	if err := q.InsertRefreshToken(r.Context(), oauthdb.InsertRefreshTokenParams{Jti: jti, UserID: user.String()}); err != nil {
		return nil, err
	}
	return &tokenResponse{
		AccessToken:  h.tokens.MintAccess(user, now),
		TokenType:    "Bearer",
		ExpiresIn:    int64(h.tokens.accessTTL / time.Second),
		RefreshToken: refresh,
	}, nil
}
