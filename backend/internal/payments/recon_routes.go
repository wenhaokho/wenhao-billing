package payments

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// registerRecon adds the reconciliation routes (Python's recon.router, mounted
// on /payments). The literal paths come before {payment_id}.
func (r routes) registerRecon(api huma.API) {
	const prefix = "/api/v1/payments"
	tags := []string{"payments"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payments-audit-log", Method: http.MethodGet, Path: prefix + "/audit-log", Tags: tags,
		Summary: "Reconciliation log entries with their payment and actor, newest first",
		Description: "payer is a case-insensitive substring of the payer name; %, _ and backslash in it match themselves. " +
			"from_date and to_date cover their whole days.",
	}), r.auditLog)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payments-audit-log-csv", Method: http.MethodGet, Path: prefix + "/audit-log.csv", Tags: tags,
		Summary: "The audit log as a CSV download, newest first",
		Description: "The same filters as the list, without limit; at most 50000 rows. A cell that starts with =, +, -, @, tab or CR " +
			"in a text column is prefixed with a single quote so a spreadsheet does not run it as a formula.",
		Responses: map[string]*huma.Response{"200": {Description: "The CSV", Content: map[string]*huma.MediaType{
			"text/csv": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
		}}},
	}), r.auditLogCSV)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payments-awaiting-review", Method: http.MethodGet, Path: prefix + "/awaiting-review", Tags: tags,
		Summary: "Payments held for manual review, newest first",
	}), r.awaitingReview)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payments-get", Method: http.MethodGet, Path: prefix + "/{payment_id}", Tags: tags,
		Summary: "One payment",
		Errors:  []int{http.StatusNotFound},
	}), r.getPayment)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payments-log", Method: http.MethodGet, Path: prefix + "/{payment_id}/log", Tags: tags,
		Summary: "A payment's reconciliation log, oldest first; empty for an unknown payment",
	}), r.paymentLog)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payments-manual-review", Method: http.MethodPost, Path: prefix + "/{payment_id}/manual-review", Tags: tags,
		Summary: "Clear a held payment against an invoice chosen by an admin",
		Description: "Locks the payment, then the invoice. The payment takes the invoice, is CLEARED and posts to the ledger; " +
			"adjustment_type is stored with no effect. Every refusal is a 400 with its sentence and changes nothing.",
		Errors: []int{http.StatusBadRequest},
	}), r.manualReview)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "payments-reverse", Method: http.MethodPost, Path: prefix + "/{payment_id}/reverse", Tags: tags,
		Summary: "Reverse a CLEARED payment",
		Description: "Reverses the payment's ledger entry, flags the payment and re-opens its invoice. " +
			"Every refusal is a 400 with its sentence and changes nothing.",
		Errors: []int{http.StatusBadRequest},
	}), r.reverse)
}

type auditIn struct {
	Action   string                            `query:"action" doc:"Exact action; empty is no filter."`
	Payer    string                            `query:"payer" doc:"Substring of the payer name; empty is no filter."`
	FromDate platform.OptionalParam[time.Time] `query:"from_date" timeFormat:"2006-01-02"`
	ToDate   platform.OptionalParam[time.Time] `query:"to_date" timeFormat:"2006-01-02"`
	Limit    int32                             `query:"limit" default:"200" minimum:"1" maximum:"500"`
}

// AuditRow is a reconciliation log entry in the audit log, in the key order
// of Python's dict.
type AuditRow struct {
	LogID        uuid.UUID      `json:"log_id"`
	PaymentID    uuid.UUID      `json:"payment_id"`
	Action       string         `json:"action"`
	Reasons      []string       `json:"reasons"`
	ReasonCodes  []string       `json:"reason_codes" nullable:"false" doc:"The code of each reason, in the same order (payments.ReasonCode)."`
	ActorUserID  *uuid.UUID     `json:"actor_user_id"`
	ActorEmail   *string        `json:"actor_email"`
	CreatedAt    *platform.Time `json:"created_at"`
	PayerName    *string        `json:"payer_name"`
	Amount       *platform.Dec4 `json:"amount" doc:"A string of 4 decimal places."`
	Currency     *string        `json:"currency"`
	InvoiceID    *uuid.UUID     `json:"invoice_id"`
	IntakeSource *string        `json:"intake_source"`
}

type auditOut struct{ Body []AuditRow }

func dateParam(p platform.OptionalParam[time.Time]) pgtype.Date {
	if !p.Set {
		return pgtype.Date{}
	}
	return pgDate(p.Value)
}

func (r routes) auditLog(ctx context.Context, in *auditIn) (*auditOut, error) {
	arg := auditParams(in.Action, in.Payer, in.FromDate, in.ToDate, in.Limit)
	rows, err := r.q.AuditLog(ctx, arg)
	if err != nil {
		return nil, err
	}
	return &auditOut{Body: auditRows(rows)}, nil
}

// auditParams are the AuditLog query's arguments for the filters both routes take.
func auditParams(action, payer string, from, to platform.OptionalParam[time.Time], limit int32) paymentsdb.AuditLogParams {
	arg := paymentsdb.AuditLogParams{FromDate: dateParam(from), ToDate: dateParam(to), RowLimit: limit}
	if action != "" {
		arg.Action = &action
	}
	if payer != "" {
		p := platform.LikeEscape(payer)
		arg.Payer = &p
	}
	return arg
}

func auditRows(rows []paymentsdb.AuditLogRow) []AuditRow {
	out := make([]AuditRow, 0, len(rows))
	for _, l := range rows {
		row := AuditRow{
			LogID: l.LogID, PaymentID: l.PaymentID, Action: l.Action, Reasons: nonNil(l.Reasons), ReasonCodes: nonNil(l.ReasonCodes),
			ActorUserID: platform.UUIDPtr(l.ActorUserID), ActorEmail: l.ActorEmail,
			PayerName: l.PayerName, Currency: l.Currency, InvoiceID: platform.UUIDPtr(l.InvoiceID), IntakeSource: l.IntakeSource,
		}
		if l.CreatedAt != nil {
			row.CreatedAt = &platform.Time{Time: *l.CreatedAt}
		}
		if l.Amount.Valid {
			a := platform.NewDec4(l.Amount.Decimal)
			row.Amount = &a
		}
		out = append(out, row)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r routes) awaitingReview(ctx context.Context, _ *struct{}) (*listOut, error) {
	rows, err := r.q.ListAwaitingReview(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]PaymentOut, 0, len(rows))
	for _, p := range rows {
		out = append(out, PaymentOutFrom(p))
	}
	return &listOut{Body: out}, nil
}

type paymentIn struct {
	PaymentID uuid.UUID `path:"payment_id"`
}

func (r routes) getPayment(ctx context.Context, in *paymentIn) (*paymentBody, error) {
	p, err := r.q.GetPayment(ctx, in.PaymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, platform.Err(http.StatusNotFound, "payment_not_found", "payment not found")
	}
	if err != nil {
		return nil, err
	}
	return &paymentBody{Body: PaymentOutFrom(p)}, nil
}

// LogEntry is one row of a payment's log, in Python's key order.
type LogEntry struct {
	LogID       uuid.UUID      `json:"log_id"`
	Action      string         `json:"action"`
	Reasons     []string       `json:"reasons"`
	ReasonCodes []string       `json:"reason_codes" nullable:"false" doc:"The code of each reason, in the same order (payments.ReasonCode)."`
	ActorUserID *uuid.UUID     `json:"actor_user_id"`
	CreatedAt   *platform.Time `json:"created_at"`
}

type logOut struct{ Body []LogEntry }

func (r routes) paymentLog(ctx context.Context, in *paymentIn) (*logOut, error) {
	rows, err := r.q.PaymentLog(ctx, in.PaymentID)
	if err != nil {
		return nil, err
	}
	out := make([]LogEntry, 0, len(rows))
	for _, l := range rows {
		e := LogEntry{LogID: l.LogID, Action: l.Action, Reasons: nonNil(l.Reasons), ReasonCodes: nonNil(l.ReasonCodes), ActorUserID: platform.UUIDPtr(l.ActorUserID)}
		if l.CreatedAt != nil {
			e.CreatedAt = &platform.Time{Time: *l.CreatedAt}
		}
		out = append(out, e)
	}
	return &logOut{Body: out}, nil
}

// ManualApproveRequest is the body of manual-review (Python's
// ManualApproveRequest).
type ManualApproveRequest struct {
	InvoiceID      uuid.UUID                `json:"invoice_id"`
	AdjustmentType platform.NotNull[string] `json:"adjustment_type" doc:"NONE (the default), PARTIAL_PAY, CREDIT_ON_ACCOUNT or BANK_FEE; stored with no effect."`
}

type manualIn struct {
	PaymentID uuid.UUID `path:"payment_id"`
	Body      ManualApproveRequest
}

var adjustmentTypes = []string{matching.AdjNone, matching.AdjPartialPay, matching.AdjCreditOnAccount, matching.AdjBankFee}

func (r routes) manualReview(ctx context.Context, in *manualIn) (*paymentBody, error) {
	adj := in.Body.AdjustmentType.Or(matching.AdjNone)
	if !slices.Contains(adjustmentTypes, adj) {
		return nil, platform.Err(http.StatusBadRequest, "unknown_adjustment_type", "unknown adjustment_type "+platform.PyRepr(adj))
	}
	actor := platform.PrincipalFrom(ctx).UserID
	return r.settle(ctx, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return r.svc.ApproveManual(ctx, tx, in.PaymentID, in.Body.InvoiceID, &actor, adj)
	})
}

// ReverseRequest is the body of reverse (Python's ReverseRequest): reason is
// required and may be empty. It is bounded at 490 characters so "Reversal:
// <reason>" (10 more) fits the journal memo's varchar(500); Python had no
// bound and answered 500.
type ReverseRequest struct {
	Reason string `json:"reason" maxLength:"490" doc:"Kept in the log and as the ledger memo \"Reversal: <reason>\"."`
}

type reverseIn struct {
	PaymentID uuid.UUID `path:"payment_id"`
	Body      ReverseRequest
}

func (r routes) reverse(ctx context.Context, in *reverseIn) (*paymentBody, error) {
	actor := platform.PrincipalFrom(ctx).UserID
	return r.settle(ctx, func(tx pgx.Tx) (paymentsdb.Payment, error) {
		return r.svc.Reverse(ctx, tx, in.PaymentID, &actor, in.Body.Reason)
	})
}

// settle runs a clearing or reversal in one transaction; a refusal rolls it
// back and is answered 400 with its sentence. Once committed, a CLEARED
// payment's hosting is restored (AfterCommit; a reversal is FLAGGED, so
// nothing).
func (r routes) settle(ctx context.Context, fn func(tx pgx.Tx) (paymentsdb.Payment, error)) (*paymentBody, error) {
	var p paymentsdb.Payment
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		p, err = fn(tx)
		return err
	})
	if err != nil {
		return nil, refusalError(err)
	}
	r.svc.AfterCommit(ctx, p)
	return &paymentBody{Body: PaymentOutFrom(p)}, nil
}

// refusalError is err as a 400 when it is a service, ledger or FX refusal;
// anything else is unchanged.
func refusalError(err error) error {
	switch {
	case errors.Is(err, ErrPayment):
		return platform.Err(http.StatusBadRequest, refusalCode(err.Error()), err.Error())
	case errors.Is(err, ledger.ErrLedger):
		return platform.Err(http.StatusBadRequest, "ledger_refused", err.Error())
	case errors.Is(err, fx.ErrRateMissing):
		return platform.Err(http.StatusBadRequest, "fx_rate_missing", err.Error())
	}
	return err
}

// refusalCode is the stable code of a service sentence.
func refusalCode(msg string) string {
	switch {
	case strings.HasPrefix(msg, "payment ") && strings.HasSuffix(msg, " not found"):
		return "payment_not_found"
	case strings.HasPrefix(msg, "invoice ") && strings.HasSuffix(msg, " not found"):
		return "invoice_not_found"
	case strings.HasSuffix(msg, " already CLEARED"):
		return "payment_already_cleared"
	case strings.HasPrefix(msg, "cannot approve payment against "):
		return "invoice_not_approvable"
	case strings.HasPrefix(msg, "only CLEARED payments can be reversed"):
		return "payment_not_reversible"
	case strings.HasPrefix(msg, "no active journal entry"):
		return "no_active_entry"
	}
	return "payment_refused"
}
