package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx/fxdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// A manual sync whose caller has no user id (no principal in the context)
// records a null actor, as the reminder routes do, instead of uuid.Nil.
func TestManualSyncWithoutUserRecordsNullActor(t *testing.T) {
	t.Parallel()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"amount":1.0,"base":"` + r.URL.Query().Get("base") + `","date":"2026-01-09","rates":{"IDR":16000}}`))
	}))
	t.Cleanup(provider.Close)
	cfg := platform.Config{
		BaseCurrency: "IDR", FXProviderBaseURL: provider.URL,
		FXProviderTimeout: 5 * time.Second, FXSyncCurrencies: []string{"USD"},
	}
	a := apitest.New(t, func(huma.API, apitest.Deps) {})
	r := routes{pool: a.Pool, q: fxdb.New(a.Pool), cfg: cfg, client: NewClient(cfg)}
	if _, err := r.sync(context.Background(), nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	var actor pgtype.UUID
	if err := a.Pool.QueryRow(t.Context(), `SELECT actor_user_id FROM fx_sync_runs`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor.Valid {
		t.Fatalf("actor_user_id = %v, want null", actor)
	}
}
