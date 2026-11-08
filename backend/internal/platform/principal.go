package platform

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/auth"
	"github.com/LOQATO/gokit/i18n"
)

// AccessCookie is the cookie a browser carries its access token in.
const AccessCookie = "access_token"

// PublicTag marks an operation that needs no signed-in principal.
const PublicTag = "public"

// Principal is who is calling: a user of one organization and the
// permissions they hold there.
type Principal struct {
	UserID      uuid.UUID
	OrgID       uuid.UUID
	Permissions map[string]bool
}

type principalKey struct{}

// PrincipalFrom is the signed-in caller. Every operation except a public one
// has one; calling this in a public operation returns the zero Principal.
func PrincipalFrom(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}

const permissionKey = "permission"

// Needs declares the permission an operation requires; the authentication
// middleware refuses a caller without it before the handler runs. Every
// operation that is not public must declare one: an operation without one is
// refused to everyone, and TestEveryOperationDeclaresAPermission fails.
func Needs(permission string, op huma.Operation) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[permissionKey] = permission
	return op
}

// PermissionOf is the permission an operation declared with Needs, or "".
func PermissionOf(op *huma.Operation) string {
	p, _ := op.Metadata[permissionKey].(string)
	return p
}

// Require refuses with 403 when the caller lacks permission. Needs covers an
// operation's own permission; Require is for a second, finer check inside a
// handler.
func Require(ctx context.Context, permission string) error {
	if PrincipalFrom(ctx).Permissions[permission] {
		return nil
	}
	return apierr.New(http.StatusForbidden, "permission_denied", i18n.T("You do not have permission to do this"))
}

// authenticate is a Huma middleware: every operation not tagged PublicTag
// needs a valid access token, from the Authorization header or the access
// cookie, naming a user and an organization.
func authenticate(api huma.API, tokens auth.Tokens) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if slices.Contains(ctx.Operation().Tags, PublicTag) {
			next(ctx)
			return
		}
		p, ok := principal(ctx, tokens)
		if !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "")
			return
		}
		// Fail closed: an operation that declared no permission is refused.
		if perm := PermissionOf(ctx.Operation()); perm == "" || !p.Permissions[perm] {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden, "")
			return
		}
		next(huma.WithValue(ctx, principalKey{}, p))
	}
}

func principal(ctx huma.Context, tokens auth.Tokens) (Principal, bool) {
	raw, ok := strings.CutPrefix(ctx.Header("Authorization"), "Bearer ")
	if !ok {
		c, err := huma.ReadCookie(ctx, AccessCookie)
		if err != nil {
			return Principal{}, false
		}
		raw = c.Value
	}
	claims, err := tokens.Parse(raw)
	// A refresh token is signed with the same secret; only an access token
	// opens the API.
	if err != nil || claims["type"] != "access" {
		return Principal{}, false
	}
	sub, _ := claims["sub"].(string)
	org, _ := claims["org"].(string)
	userID, err1 := uuid.Parse(sub)
	orgID, err2 := uuid.Parse(org)
	if err1 != nil || err2 != nil {
		return Principal{}, false
	}
	perms := map[string]bool{}
	if list, ok := claims["perms"].([]any); ok {
		for _, p := range list {
			if s, ok := p.(string); ok {
				perms[s] = true
			}
		}
	}
	return Principal{UserID: userID, OrgID: orgID, Permissions: perms}, true
}

// AccessClaims are the claims an access token for p carries.
func AccessClaims(p Principal) jwt.MapClaims {
	perms := make([]string, 0, len(p.Permissions))
	for k, v := range p.Permissions {
		if v {
			perms = append(perms, k)
		}
	}
	slices.Sort(perms)
	return jwt.MapClaims{"sub": p.UserID.String(), "org": p.OrgID.String(), "perms": perms, "type": "access"}
}

// TestToken signs an access token for p, for tests.
func TestToken(t testing.TB, secret string, p Principal) string {
	t.Helper()
	tok, err := auth.Tokens{Secret: []byte(secret), AccessTTL: time.Hour}.Issue(AccessClaims(p))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
