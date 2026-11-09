package business

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// allowedCurrencies are the currencies a payment account may be in.
var allowedCurrencies = map[string]bool{"IDR": true, "SGD": true, "USD": true}

type routes struct {
	pool  *pgxpool.Pool
	q     *businessdb.Queries
	clock platform.Clock
}

// Register adds the business profile and payment account operations; every
// one is admin-only. clock dates the profile GET answers while row 1 is
// missing.
func Register(api huma.API, pool *pgxpool.Pool, clock platform.Clock) {
	r := routes{pool: pool, q: businessdb.New(pool), clock: clock}
	const prefix = "/api/v1/business-profile"
	tags := []string{"business-profile"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "business-profile-get", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "The business profile",
	}), r.getProfile)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "business-profile-update", Method: http.MethodPut, Path: prefix, Tags: tags,
		Summary: "Change the business profile's fields",
	}), r.updateProfile)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payment-accounts-list", Method: http.MethodGet, Path: prefix + "/payment-accounts", Tags: tags,
		Summary: "List the payment accounts",
	}), r.listAccounts)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payment-accounts-replace", Method: http.MethodPut, Path: prefix + "/payment-accounts", Tags: tags,
		Summary: "Replace every payment account", Errors: []int{http.StatusUnprocessableEntity},
	}), r.replaceAccounts)
}

type profileBody struct{ Body ProfileOut }

func (r routes) getProfile(ctx context.Context, _ *struct{}) (*profileBody, error) {
	p, err := Profile(ctx, r.q, r.clock.Now())
	if err != nil {
		return nil, err
	}
	return &profileBody{Body: p}, nil
}

type updateIn struct{ Body ProfileUpdate }

// updateProfile creates row 1 if it is missing, then writes the fields sent.
// updated_at moves only when a field was sent, as SQLAlchemy's onupdate
// fires only on a real UPDATE.
func (r routes) updateProfile(ctx context.Context, in *updateIn) (*profileBody, error) {
	b := in.Body
	var p businessdb.BusinessProfile
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.EnsureProfile(ctx); err != nil {
			return err
		}
		cur, err := q.GetProfileForUpdate(ctx)
		if err != nil {
			return err
		}
		if !b.anySet() {
			p = cur
			return nil
		}
		p, err = q.UpdateProfile(ctx, businessdb.UpdateProfileParams{
			Name:           b.Name.Merge(cur.Name),
			Address:        b.Address.Merge(cur.Address),
			ContactEmail:   b.ContactEmail.Merge(cur.ContactEmail),
			ContactPhone:   b.ContactPhone.Merge(cur.ContactPhone),
			InvoiceTitle:   b.InvoiceTitle.Merge(cur.InvoiceTitle),
			InvoiceSummary: b.InvoiceSummary.Merge(cur.InvoiceSummary),
			LogoUrl:        b.LogoURL.Merge(cur.LogoUrl),
			DefaultNotes:   b.DefaultNotes.Merge(cur.DefaultNotes),
			Npwp:           b.Npwp.Merge(cur.Npwp),
			NpwpName:       b.NpwpName.Merge(cur.NpwpName),
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &profileBody{Body: profileOut(p)}, nil
}

type accountsBody struct{ Body []PaymentAccountOut }

func (r routes) listAccounts(ctx context.Context, _ *struct{}) (*accountsBody, error) {
	out, err := PaymentAccounts(ctx, r.q)
	if err != nil {
		return nil, err
	}
	return &accountsBody{Body: out}, nil
}

type replaceIn struct{ Body []PaymentAccountIn }

// checkAccounts refuses a list in Python's order, on the upper-cased
// currencies: a repeated currency, then currencies outside IDR, SGD and USD,
// then more than one default. It returns the upper-cased currencies.
func checkAccounts(accounts []PaymentAccountIn) ([]string, error) {
	currencies := make([]string, len(accounts))
	seen := map[string]bool{}
	for i, a := range accounts {
		currencies[i] = strings.ToUpper(a.Currency)
		if seen[currencies[i]] {
			return nil, platform.Err(http.StatusUnprocessableEntity, "duplicate_currency", "Duplicate currency in payment accounts")
		}
		seen[currencies[i]] = true
	}
	var unsupported []string
	for _, c := range currencies {
		if !allowedCurrencies[c] {
			unsupported = append(unsupported, c)
		}
	}
	if len(unsupported) > 0 {
		slices.Sort(unsupported)
		return nil, platform.Err(http.StatusUnprocessableEntity, "unsupported_currency",
			"Unsupported currency: "+strings.Join(unsupported, ", "))
	}
	defaults := 0
	for _, a := range accounts {
		if a.IsDefault.Or(false) {
			defaults++
		}
	}
	if defaults > 1 {
		return nil, platform.Err(http.StatusUnprocessableEntity, "multiple_default_accounts", "At most one account can be the default")
	}
	return currencies, nil
}

// replaceAccounts deletes every account and inserts the list, holding row 1
// locked so two replaces run one after the other: interleaved, both would
// delete before either inserts, and the second insert would hit
// uq_payment_account_currency.
func (r routes) replaceAccounts(ctx context.Context, in *replaceIn) (*accountsBody, error) {
	currencies, err := checkAccounts(in.Body)
	if err != nil {
		return nil, err
	}
	var out []PaymentAccountOut
	err = db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.EnsureProfile(ctx); err != nil {
			return err
		}
		if _, err := q.GetProfileForUpdate(ctx); err != nil {
			return err
		}
		if err := q.DeletePaymentAccounts(ctx); err != nil {
			return err
		}
		for i, a := range in.Body {
			if err := q.CreatePaymentAccount(ctx, businessdb.CreatePaymentAccountParams{
				Currency: currencies[i], Instructions: a.Instructions, IsDefault: a.IsDefault.Or(false),
			}); err != nil {
				return err
			}
		}
		out, err = PaymentAccounts(ctx, q)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &accountsBody{Body: out}, nil
}
