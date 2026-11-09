// Package oauth holds the MCP connector's OAuth server. This file is its
// token layer: the HS256 JWTs for access, refresh and authorization codes,
// byte-compatible with the ones the Python backend minted, so a token issued
// before the switch still verifies after it.
package oauth

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// codeTTL is how long an authorization code lives (Python's _CODE_TTL_SECONDS).
const codeTTL = 120 * time.Second

const (
	typAccess  = "access"
	typRefresh = "refresh"
	typCode    = "code"
)

// TokenError is a token that is malformed, expired, tampered with or of the
// wrong type. Its text is PyJWT's sentence where PyJWT has one, because the
// OAuth endpoints echo it in error_description.
type TokenError struct{ msg string }

func (e *TokenError) Error() string { return e.msg }

func tokErr(msg string) error { return &TokenError{msg: msg} }

// Tokens mints and verifies the three token kinds. It holds the secret and
// the two configurable lifetimes; the clock is passed per call so callers use
// platform.Clock and tests fix the time.
type Tokens struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokens returns a Tokens signing with secret (MCP_TOKEN_SECRET).
func NewTokens(secret string, accessTTL, refreshTTL time.Duration) *Tokens {
	return &Tokens{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}
}

// The claim structs list their fields in Python's insertion order, so the
// encoded payload is byte for byte what PyJWT produced.
type accessClaims struct {
	Sub string `json:"sub"`
	Typ string `json:"typ"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

type refreshClaims struct {
	Sub string `json:"sub"`
	Typ string `json:"typ"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`
}

type codeWire struct {
	Typ           string `json:"typ"`
	Sub           string `json:"sub"`
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	CodeChallenge string `json:"code_challenge"`
	Iat           int64  `json:"iat"`
	Exp           int64  `json:"exp"`
}

// CodeClaims is what an authorization code binds: the user who approved and
// the client, redirect URI and PKCE challenge the code is redeemable with.
type CodeClaims struct {
	UserID        uuid.UUID
	ClientID      string
	RedirectURI   string
	CodeChallenge string
}

// plain adapts a claims struct to golang-jwt's signer, which only encodes it.
// All validation is done by decode, following PyJWT's rules.
type plain struct{ v any }

func (p plain) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // PyJWT leaves < > & alone
	if err := enc.Encode(p.v); err != nil {
		return nil, fmt.Errorf("encode claims: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func (plain) GetExpirationTime() (*jwt.NumericDate, error) { return nil, nil } //nolint:nilnil // interface stub
func (plain) GetIssuedAt() (*jwt.NumericDate, error)       { return nil, nil } //nolint:nilnil // interface stub
func (plain) GetNotBefore() (*jwt.NumericDate, error)      { return nil, nil } //nolint:nilnil // interface stub
func (plain) GetIssuer() (string, error)                   { return "", nil }
func (plain) GetSubject() (string, error)                  { return "", nil }
func (plain) GetAudience() (jwt.ClaimStrings, error)       { return nil, nil }

func (t *Tokens) sign(claims any) string {
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, plain{claims}).SignedString(t.secret)
	if err != nil {
		// HS256 with a byte-slice key cannot fail to sign.
		panic(fmt.Sprintf("oauth: signing a token: %v", err))
	}
	return s
}

// MintAccess issues an access token for the user, valid for the access TTL.
func (t *Tokens) MintAccess(sub uuid.UUID, now time.Time) string {
	return t.sign(accessClaims{Sub: sub.String(), Typ: typAccess, Iat: now.Unix(), Exp: now.Add(t.accessTTL).Unix()})
}

// MintRefresh issues a refresh token with a fresh jti, returned so the caller
// can store it for revocation.
func (t *Tokens) MintRefresh(sub uuid.UUID, now time.Time) (token, jti string) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("oauth: random jti: %v", err))
	}
	jti = hex.EncodeToString(b[:])
	return t.MintRefreshWithJTI(sub, jti, now), jti
}

// MintRefreshWithJTI is MintRefresh with the jti chosen by the caller.
func (t *Tokens) MintRefreshWithJTI(sub uuid.UUID, jti string, now time.Time) string {
	return t.sign(refreshClaims{Sub: sub.String(), Typ: typRefresh, Iat: now.Unix(), Exp: now.Add(t.refreshTTL).Unix(), Jti: jti})
}

// MintCode issues an authorization code, valid for 120 seconds.
func (t *Tokens) MintCode(c CodeClaims, now time.Time) string {
	return t.sign(codeWire{
		Typ: typCode, Sub: c.UserID.String(), ClientID: c.ClientID, RedirectURI: c.RedirectURI,
		CodeChallenge: c.CodeChallenge, Iat: now.Unix(), Exp: now.Add(codeTTL).Unix(),
	})
}

// decode checks the structure, algorithm, signature, iat and exp, in PyJWT's
// order, and returns the claims. It does not check typ.
func (t *Tokens) decode(token string, now time.Time) (jwt.MapClaims, error) {
	if strings.Count(token, ".") < 2 {
		return nil, tokErr("Not enough segments")
	}
	// Only HS256 is accepted: "none" and every other algorithm are refused
	// before any key is touched.
	var unverified jwt.MapClaims
	parsed, _, err := jwt.NewParser().ParseUnverified(token, &unverified)
	if err != nil {
		return nil, tokErr("Invalid token")
	}
	if alg, _ := parsed.Header["alg"].(string); alg != jwt.SigningMethodHS256.Alg() {
		return nil, tokErr("The specified alg value is not allowed")
	}
	claims := jwt.MapClaims{}
	_, err = jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithoutClaimsValidation(),
		jwt.WithJSONNumber(),
	).ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return t.secret, nil })
	if err != nil {
		if errors.Is(err, jwt.ErrTokenSignatureInvalid) {
			return nil, tokErr("Signature verification failed")
		}
		return nil, tokErr("Invalid token")
	}
	n := now.Unix()
	if iat, ok := numeric(claims["iat"]); ok && iat > n {
		return nil, tokErr("The token is not yet valid (iat)")
	}
	exp, ok := numeric(claims["exp"])
	if !ok {
		// PyJWT would let a token without exp through; the issuer never
		// omits it, so a token without one is a forgery or a bug.
		return nil, tokErr("Token is missing the \"exp\" claim")
	}
	if exp <= n {
		return nil, tokErr("Signature has expired")
	}
	return claims, nil
}

// numeric reads a JSON integer claim as whole seconds; PyJWT also insists on
// an integer.
func numeric(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

// subject parses the sub claim. A non-UUID, absent or non-string sub is a
// token error: Python let it escape as a 500, Go refuses the token.
func subject(c jwt.MapClaims) (uuid.UUID, error) {
	s, _ := c["sub"].(string)
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, tokErr("invalid sub claim")
	}
	return id, nil
}

func str(c jwt.MapClaims, k string) string { s, _ := c[k].(string); return s }

// VerifyAccess returns the user an access token was issued to.
func (t *Tokens) VerifyAccess(token string, now time.Time) (uuid.UUID, error) {
	c, err := t.decode(token, now)
	if err != nil {
		return uuid.Nil, err
	}
	if str(c, "typ") != typAccess {
		return uuid.Nil, tokErr("not an access token")
	}
	return subject(c)
}

// VerifyRefresh returns the user and jti of a refresh token. Revocation needs
// the database and is the caller's job.
func (t *Tokens) VerifyRefresh(token string, now time.Time) (uuid.UUID, string, error) {
	c, err := t.decode(token, now)
	if err != nil {
		return uuid.Nil, "", err
	}
	if str(c, "typ") != typRefresh {
		return uuid.Nil, "", tokErr("not a refresh token")
	}
	jti := str(c, "jti")
	if jti == "" {
		return uuid.Nil, "", tokErr("refresh token missing jti")
	}
	sub, err := subject(c)
	if err != nil {
		return uuid.Nil, "", err
	}
	return sub, jti, nil
}

// VerifyCode returns the claims of an authorization code.
func (t *Tokens) VerifyCode(token string, now time.Time) (CodeClaims, error) {
	c, _, err := t.verifyCode(token, now)
	return c, err
}

// verifyCode is VerifyCode plus the code's expiry, which the token endpoint
// keeps with the spent code so its row outlives the code.
func (t *Tokens) verifyCode(token string, now time.Time) (CodeClaims, time.Time, error) {
	c, err := t.decode(token, now)
	if err != nil {
		return CodeClaims{}, time.Time{}, err
	}
	if str(c, "typ") != typCode {
		return CodeClaims{}, time.Time{}, tokErr("not an authorization code")
	}
	sub, err := subject(c)
	if err != nil {
		return CodeClaims{}, time.Time{}, err
	}
	exp, _ := numeric(c["exp"]) // decode has checked it is there
	return CodeClaims{UserID: sub, ClientID: str(c, "client_id"), RedirectURI: str(c, "redirect_uri"), CodeChallenge: str(c, "code_challenge")},
		time.Unix(exp, 0).UTC(), nil
}
