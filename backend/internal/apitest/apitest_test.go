package apitest_test

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/auth"
)

func TestApitestAdminReachesAdminRoute(t *testing.T) {
	t.Parallel()
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

func TestApitestSeedsPythonRates(t *testing.T) {
	t.Parallel()
	a := apitest.New(t, func(huma.API, apitest.Deps) {})
	rows, err := a.Pool.Query(t.Context(),
		`SELECT from_currency || '>' || to_currency || '=' || rate::text || '@' || as_of_date::text || '/' || source FROM fx_rates WHERE source = 'TEST' ORDER BY from_currency`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	want := []string{"SGD>IDR=12000.00000000@2026-01-01/TEST", "USD>IDR=16000.00000000@2026-01-01/TEST"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("fx_rates = %v, want %v", got, want)
	}
}
