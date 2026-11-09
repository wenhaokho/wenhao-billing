package apitest_test

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/auth"
)

func TestApitestAdminReachesAdminRoute(t *testing.T) {
	a := apitest.New(t, func(api huma.API, d apitest.Deps) {
		auth.Register(api, d.Pool, d.Sessions, d.Mail, d.Config, d.Clock)
	})
	me := apitest.Decode[auth.UserOut](t, a.Do(t, http.MethodGet, "/api/v1/auth/me", nil), http.StatusOK)
	if me.UserID != a.AdminID || me.Role != "admin" {
		t.Errorf("me: %+v, want admin %s", me, a.AdminID)
	}
	if rec := a.Anon(t, http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d %s", rec.Code, rec.Body)
	}
}
