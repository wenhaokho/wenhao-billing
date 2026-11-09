package oauth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
)

const (
	testSecret = "unit-test-token-secret-0123456789"
	accessTTL  = 3600 * time.Second
	refreshTTL = 2592000 * time.Second
)

func newTokens() *oauth.Tokens { return oauth.NewTokens(testSecret, accessTTL, refreshTTL) }

// wantErr fails unless err is a token error with exactly this sentence.
func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %q, got nil", want)
	}
	var te *oauth.TokenError
	if !errors.As(err, &te) {
		t.Fatalf("want *TokenError, got %T: %v", err, err)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// forge signs an arbitrary header and claims with secret, as an attacker or a
// buggy issuer would.
func forge(t *testing.T, secret string, header, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	h, err := json.Marshal(header)
	noErr(t, err)
	c, err := json.Marshal(claims)
	noErr(t, err)
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	return signing + "." + enc.EncodeToString(mac.Sum(nil))
}

var hs256 = map[string]any{"alg": "HS256", "typ": "JWT"}

func TestAccessTokenRoundTrip(t *testing.T) {
	t.Parallel()
	uid := uuid.New()
	now := time.Now()
	got, err := newTokens().VerifyAccess(newTokens().MintAccess(uid, now), now)
	noErr(t, err)
	if got != uid {
		t.Fatalf("sub = %v, want %v", got, uid)
	}
}

func TestExpiredAccessTokenRejected(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	tok := tk.MintAccess(uuid.New(), time.Unix(1, 0))
	_, err := tk.VerifyAccess(tok, time.Now())
	wantErr(t, err, "Signature has expired")
}

func TestExpiryBoundary(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	iat := time.Unix(1_800_000_000, 0)
	tok := tk.MintAccess(uuid.New(), iat)
	_, err := tk.VerifyAccess(tok, iat.Add(accessTTL-time.Second))
	noErr(t, err)
	// PyJWT treats exp <= now as expired.
	_, err = tk.VerifyAccess(tok, iat.Add(accessTTL))
	wantErr(t, err, "Signature has expired")
}

func TestTamperedTokenRejected(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	now := time.Now()
	tok := tk.MintAccess(uuid.New(), now)
	_, err := tk.VerifyAccess(tok+"x", now)
	wantErr(t, err, "Signature verification failed")
	other := oauth.NewTokens("another-secret-0123456789abcdef", accessTTL, refreshTTL)
	_, err = other.VerifyAccess(tok, now)
	wantErr(t, err, "Signature verification failed")
}

func TestRefreshTokenNotAcceptedAsAccess(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	now := time.Now()
	tok, _ := tk.MintRefresh(uuid.New(), now)
	_, err := tk.VerifyAccess(tok, now)
	wantErr(t, err, "not an access token")
}

func TestTokenTypesAreNotInterchangeable(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	now := time.Now()
	uid := uuid.New()
	access := tk.MintAccess(uid, now)
	code := tk.MintCode(oauth.CodeClaims{UserID: uid, ClientID: "c", RedirectURI: "https://x/cb", CodeChallenge: "ch"}, now)
	_, _, err := tk.VerifyRefresh(access, now)
	wantErr(t, err, "not a refresh token")
	_, err = tk.VerifyAccess(code, now)
	wantErr(t, err, "not an access token")
	_, err = tk.VerifyCode(access, now)
	wantErr(t, err, "not an authorization code")
}

func TestRefreshRoundTripAndJTI(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	now := time.Now()
	uid := uuid.New()
	tok, jti := tk.MintRefresh(uid, now)
	if len(jti) != 32 {
		t.Fatalf("jti %q is not 32 hex", jti)
	}
	gotUID, gotJTI, err := tk.VerifyRefresh(tok, now)
	noErr(t, err)
	if gotUID != uid || gotJTI != jti {
		t.Fatalf("got %v %q, want %v %q", gotUID, gotJTI, uid, jti)
	}
	if _, jti2 := tk.MintRefresh(uid, now); jti2 == jti {
		t.Fatal("two refresh tokens share a jti")
	}
}

func TestRefreshMissingJTI(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tok := forge(t, testSecret, hs256, map[string]any{
		"sub": uuid.NewString(), "typ": "refresh", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	_, _, err := newTokens().VerifyRefresh(tok, now)
	wantErr(t, err, "refresh token missing jti")
}

func TestCodeRoundTrip(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	now := time.Now()
	in := oauth.CodeClaims{UserID: uuid.New(), ClientID: "client-1", RedirectURI: "https://example.com/cb", CodeChallenge: "chal"}
	tok := tk.MintCode(in, now)
	got, err := tk.VerifyCode(tok, now)
	noErr(t, err)
	if got != in {
		t.Fatalf("claims = %+v, want %+v", got, in)
	}
	// A code lives 120 s.
	_, err = tk.VerifyCode(tok, now.Add(121*time.Second))
	wantErr(t, err, "Signature has expired")
}

func TestPythonMintedTokensVerify(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/python_tokens.json")
	noErr(t, err)
	var g struct {
		Secret  string `json:"secret"`
		Sub     string `json:"sub"`
		Iat     int64  `json:"iat"`
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
		JTI     string `json:"jti"`
		Code    string `json:"code"`
	}
	noErr(t, json.Unmarshal(raw, &g))
	tk := oauth.NewTokens(g.Secret, accessTTL, refreshTTL)
	sub := uuid.MustParse(g.Sub)
	now := time.Unix(g.Iat, 0).Add(time.Minute)
	wantCode := oauth.CodeClaims{UserID: sub, ClientID: "client-1", RedirectURI: "https://example.com/cb", CodeChallenge: "chal-xyz"}

	t.Run("access", func(t *testing.T) {
		t.Parallel()
		got, err := tk.VerifyAccess(g.Access, now)
		noErr(t, err)
		if got != sub {
			t.Fatalf("sub = %v", got)
		}
	})
	t.Run("refresh", func(t *testing.T) {
		t.Parallel()
		got, jti, err := tk.VerifyRefresh(g.Refresh, now)
		noErr(t, err)
		if got != sub || jti != g.JTI {
			t.Fatalf("got %v %q", got, jti)
		}
	})
	t.Run("code", func(t *testing.T) {
		t.Parallel()
		got, err := tk.VerifyCode(g.Code, now)
		noErr(t, err)
		if got != wantCode {
			t.Fatalf("claims = %+v", got)
		}
	})
	t.Run("expiry follows the claims", func(t *testing.T) {
		t.Parallel()
		_, err := tk.VerifyAccess(g.Access, time.Unix(g.Iat, 0).Add(accessTTL))
		wantErr(t, err, "Signature has expired")
	})
	// Go mints the very bytes Python does for the same claims, so tokens
	// issued after the switch verify on any Python replica still running.
	t.Run("go mints pythons bytes", func(t *testing.T) {
		t.Parallel()
		iat := time.Unix(g.Iat, 0)
		if got := tk.MintAccess(sub, iat); got != g.Access {
			t.Fatalf("access differs:\n%s\n%s", got, g.Access)
		}
		if got := tk.MintCode(wantCode, iat); got != g.Code {
			t.Fatalf("code differs:\n%s\n%s", got, g.Code)
		}
		if got := tk.MintRefreshWithJTI(sub, g.JTI, iat); got != g.Refresh {
			t.Fatalf("refresh differs:\n%s\n%s", got, g.Refresh)
		}
	})
}

func TestNonUUIDSubIsTokenError(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tk := newTokens()
	base := func(typ string) map[string]any {
		return map[string]any{"sub": "not-a-uuid", "typ": typ, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "jti": "abc"}
	}
	_, err := tk.VerifyAccess(forge(t, testSecret, hs256, base("access")), now)
	wantErr(t, err, "invalid sub claim")
	_, _, err = tk.VerifyRefresh(forge(t, testSecret, hs256, base("refresh")), now)
	wantErr(t, err, "invalid sub claim")
	c := base("code")
	_, err = tk.VerifyCode(forge(t, testSecret, hs256, c), now)
	wantErr(t, err, "invalid sub claim")
	// A missing sub is no different.
	delete(c, "sub")
	_, err = tk.VerifyCode(forge(t, testSecret, hs256, c), now)
	wantErr(t, err, "invalid sub claim")
	// And a sub of the wrong JSON type.
	c["sub"] = 7
	_, err = tk.VerifyCode(forge(t, testSecret, hs256, c), now)
	wantErr(t, err, "invalid sub claim")
}

func TestAlgNoneAndOtherAlgsRejected(t *testing.T) {
	t.Parallel()
	now := time.Now()
	claims := map[string]any{"sub": uuid.NewString(), "typ": "access", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	enc := base64.RawURLEncoding
	h, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	none := enc.EncodeToString(h) + "." + enc.EncodeToString(c) + "."
	_, err := newTokens().VerifyAccess(none, now)
	wantErr(t, err, "The specified alg value is not allowed")

	hs512 := forge(t, testSecret, map[string]any{"alg": "HS512", "typ": "JWT"}, claims)
	_, err = newTokens().VerifyAccess(hs512, now)
	wantErr(t, err, "The specified alg value is not allowed")
}

func TestFutureIatRejected(t *testing.T) {
	t.Parallel()
	tk := newTokens()
	now := time.Now()
	tok := tk.MintAccess(uuid.New(), now.Add(time.Hour))
	_, err := tk.VerifyAccess(tok, now)
	wantErr(t, err, "The token is not yet valid (iat)")
}

func TestMalformedTokens(t *testing.T) {
	t.Parallel()
	for name, tok := range map[string]string{"empty": "", "one segment": "abc", "two segments": "abc.def"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := newTokens().VerifyAccess(tok, time.Now())
			wantErr(t, err, "Not enough segments")
		})
	}
	t.Run("garbage segments", func(t *testing.T) {
		t.Parallel()
		_, err := newTokens().VerifyAccess(strings.Repeat("!.", 2)+"!", time.Now())
		if err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestMissingExpiryRejected(t *testing.T) {
	t.Parallel()
	now := time.Now()
	tok := forge(t, testSecret, hs256, map[string]any{"sub": uuid.NewString(), "typ": "access", "iat": now.Unix()})
	_, err := newTokens().VerifyAccess(tok, now)
	if err == nil {
		t.Fatal("a token without exp verified")
	}
}
