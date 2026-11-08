package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/LOQATO/gokit/auth"
	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

type whoami struct {
	Body struct {
		UserID string `json:"user_id"`
		OrgID  string `json:"org_id"`
	}
}

// serverWithWhoami is the app with one protected operation that reports the
// principal and requires "things.read".
func serverWithWhoami(t *testing.T) http.Handler {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	h, api := NewServer(Config{Environment: "test"}, Deps{
		Pool:   pool,
		Tokens: auth.Tokens{Secret: []byte(testSecret), AccessTTL: time.Minute, Leeway: 10 * time.Second},
	})
	huma.Register(api, Needs("things.read", huma.Operation{OperationID: "whoami", Method: http.MethodGet, Path: "/whoami"}), func(ctx context.Context, _ *struct{}) (*whoami, error) {
		p := PrincipalFrom(ctx)
		out := &whoami{}
		out.Body.UserID, out.Body.OrgID = p.UserID.String(), p.OrgID.String()
		return out, nil
	})
	// An operation that forgot to declare a permission is refused to
	// everyone (fail closed), never open to every signed-in user.
	huma.Register(api, Needs("things.read", huma.Operation{OperationID: "panics", Method: http.MethodGet, Path: "/panics"}), func(context.Context, *struct{}) (*whoami, error) {
		var p *Principal
		_ = p.UserID // a nil pointer, as a real bug would be
		return nil, nil
	})
	huma.Get(api, "/undeclared", func(context.Context, *struct{}) (*whoami, error) { return &whoami{}, nil })
	return h
}

func code(t *testing.T, body []byte) string {
	t.Helper()
	var b struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(body, &b)
	return b.Error.Code
}

var alice = Principal{UserID: uuid.New(), OrgID: uuid.New(), Permissions: map[string]bool{"things.read": true}}

func TestNoTokenIs401(t *testing.T) {
	rec := get(t, serverWithWhoami(t), "/whoami")
	if rec.Code != 401 || code(t, rec.Body.Bytes()) != "unauthenticated" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestBearerTokenNamesThePrincipal(t *testing.T) {
	rec := get(t, serverWithWhoami(t), "/whoami", "Authorization", "Bearer "+TestToken(t, testSecret, alice))
	var out struct{ UserID, OrgID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &struct {
		UserID *string `json:"user_id"`
		OrgID  *string `json:"org_id"`
	}{&out.UserID, &out.OrgID})
	if rec.Code != 200 || out.UserID != alice.UserID.String() || out.OrgID != alice.OrgID.String() {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestCookieTokenWorks(t *testing.T) {
	rec := get(t, serverWithWhoami(t), "/whoami", "Cookie", AccessCookie+"="+TestToken(t, testSecret, alice))
	if rec.Code != 200 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExpiredTokenIs401(t *testing.T) {
	tok := sign(t, jwt.MapClaims{
		"sub": alice.UserID.String(), "org": alice.OrgID.String(), "type": "access",
		"iat": time.Now().Add(-time.Hour).Unix(), "exp": time.Now().Add(-time.Minute).Unix(),
	})
	if rec := get(t, serverWithWhoami(t), "/whoami", "Authorization", "Bearer "+tok); rec.Code != 401 {
		t.Errorf("%d", rec.Code)
	}
}

// A refresh token is signed with the same secret; it must not open the API.
func TestRefreshTokenIs401(t *testing.T) {
	tok := sign(t, jwt.MapClaims{
		"sub": alice.UserID.String(), "org": alice.OrgID.String(), "type": "refresh",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	if rec := get(t, serverWithWhoami(t), "/whoami", "Authorization", "Bearer "+tok); rec.Code != 401 {
		t.Errorf("%d", rec.Code)
	}
}

func TestMissingPermissionIs403(t *testing.T) {
	bob := Principal{UserID: uuid.New(), OrgID: uuid.New(), Permissions: map[string]bool{"other.read": true}}
	rec := get(t, serverWithWhoami(t), "/whoami", "Authorization", "Bearer "+TestToken(t, testSecret, bob), "Accept-Language", "id")
	if rec.Code != 403 || code(t, rec.Body.Bytes()) != "permission_denied" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestHealthStaysPublic(t *testing.T) {
	if rec := get(t, serverWithWhoami(t), "/health"); rec.Code != 200 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestAnOperationWithoutADeclaredPermissionIsRefused(t *testing.T) {
	rec := get(t, serverWithWhoami(t), "/undeclared", "Authorization", "Bearer "+TestToken(t, testSecret, alice))
	if rec.Code != 403 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// A panic is a 500 in the error envelope (the frontend can read it), never a
// bare body, and the server keeps serving.
func TestAPanicIsAnEnvelope500(t *testing.T) {
	h := serverWithWhoami(t)
	rec := get(t, h, "/panics", "Authorization", "Bearer "+TestToken(t, testSecret, alice), "Accept-Language", "id")
	if rec.Code != 500 || code(t, rec.Body.Bytes()) != "internal_error" || !strings.Contains(rec.Body.String(), "Terjadi kesalahan tak terduga") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/health"); rec.Code != 200 {
		t.Errorf("after a panic: %d", rec.Code)
	}
}
