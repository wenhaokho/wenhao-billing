package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// connector is the MCP connector's OAuth server on the app's database, for
// the tests that check a password change signs the connector out too.
type connector struct {
	h      http.Handler
	tokens *oauth.Tokens
	clock  platform.Clock
}

const connectorVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

func newConnector(a app) connector {
	tokens := oauth.NewTokens("auth-test-mcp-token-secret", time.Hour, 30*24*time.Hour)
	r := chi.NewMux()
	oauth.Mount(r, oauth.Deps{
		Pool: a.pool, PublicBaseURL: "https://app.example.com", AppBaseURL: "https://app.example.com/",
		Sessions: platform.NewSessions(a.cfg), Tokens: tokens, Clock: a.clock,
	})
	return connector{h: r, tokens: tokens, clock: a.clock}
}

func (c connector) post(path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

// pair registers a client, exchanges a code for user and returns the client
// id and the refresh token.
func (c connector) pair(t *testing.T, user uuid.UUID) (clientID, refresh string) {
	t.Helper()
	const redirect = "http://localhost/cb"
	rec := c.post("/oauth/register", "application/json", `{"client_name":"cli","redirect_uris":["`+redirect+`"]}`)
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &reg) != nil {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	sum := sha256.Sum256([]byte(connectorVerifier))
	code := c.tokens.MintCode(oauth.CodeClaims{
		UserID: user, ClientID: reg.ClientID, RedirectURI: redirect,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, c.clock.Now())
	rec = c.post("/oauth/token", "application/x-www-form-urlencoded", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {reg.ClientID}, "code_verifier": {connectorVerifier},
	}.Encode())
	var tok struct {
		RefreshToken string `json:"refresh_token"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &tok) != nil || tok.RefreshToken == "" {
		t.Fatalf("code exchange: %d %s", rec.Code, rec.Body)
	}
	return reg.ClientID, tok.RefreshToken
}

func (c connector) refresh(clientID, refresh string) *httptest.ResponseRecorder {
	return c.post("/oauth/token", "application/x-www-form-urlencoded", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID},
	}.Encode())
}

// wantRefreshRefused fails unless rec is the refresh grant's existing refusal
// of a revoked token.
func wantRefreshRefused(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	want := `{"error":"invalid_grant","error_description":"refresh token reuse detected"}`
	if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("refresh after the password change: %d %s, want 400 %s", rec.Code, rec.Body, want)
	}
}

// A password change signs out everywhere, the MCP connector included: its
// refresh tokens are revoked with the epoch move.
func TestChangePasswordRevokesConnectorRefreshTokens(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	c := newConnector(a)
	cid, refresh := c.pair(t, id)
	// Someone else's connector is untouched.
	otherID := a.user(t, "other-admin@example.com", "admin")
	otherCID, otherRefresh := c.pair(t, otherID)

	cookie := a.mustLogin(t, adminEmail, false)
	if rec := a.changePassword(t, cookie, password, "brand-new-pw"); rec.Code != http.StatusOK {
		t.Fatalf("change-password: %d %s", rec.Code, rec.Body)
	}
	wantRefreshRefused(t, c.refresh(cid, refresh))
	if rec := c.refresh(otherCID, otherRefresh); rec.Code != http.StatusOK {
		t.Fatalf("another user's refresh: %d %s, want 200", rec.Code, rec.Body)
	}
}

// A password reset signs the connector out the same way.
func TestResetPasswordRevokesConnectorRefreshTokens(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	c := newConnector(a)
	cid, refresh := c.pair(t, id)

	if rec := a.reset(t, a.forgotToken(t), "brand-new-pw"); rec.Code != http.StatusOK {
		t.Fatalf("reset-password: %d %s", rec.Code, rec.Body)
	}
	wantRefreshRefused(t, c.refresh(cid, refresh))
}
