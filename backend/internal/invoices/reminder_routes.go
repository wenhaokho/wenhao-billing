package invoices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/i18n"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

type reminderRoutes struct {
	pool  *pgxpool.Pool
	mail  email.Sender
	clock platform.Clock
}

// RegisterReminders adds POST and GET /invoices/{invoice_id}/reminders, both
// admin-only. A nil mail logs each message (email.LogSender).
func RegisterReminders(api huma.API, pool *pgxpool.Pool, clock platform.Clock, mail email.Sender) {
	if mail == nil {
		mail = email.LogSender{}
	}
	r := reminderRoutes{pool: pool, mail: mail, clock: clock}
	const path = "/api/v1/invoices/{invoice_id}/reminders"
	tags := []string{"invoices"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-send-reminder", Method: http.MethodPost, Path: path, Tags: tags,
		Summary:       "Email a payment reminder for an outstanding invoice and log it",
		Description:   "Refuses a VOID or PAID invoice, a recurring template, a DRAFT and a send with no recipient (422). A reminder already sent to the same customer in the last 24 hours is 409 (the details name the earlier invoice_number and sent_at) unless force is true. An expected_amount that is not the cash still expected is 409 reminder_amount_changed (the details name the current amount and currency); nothing is sent. A send failure is 502 and logs nothing.",
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusBadGateway},
	}), r.send)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-list-reminders", Method: http.MethodGet, Path: path, Tags: tags,
		Summary: "The invoice's reminder history, latest first",
		Errors:  []int{http.StatusNotFound},
	}), r.list)
}

// SendReminderRequest is the body of POST .../reminders.
type SendReminderRequest struct {
	ToEmail        platform.Optional[string]        `json:"to_email" doc:"The recipient; empty or null falls back to the customer's contact email."`
	Force          bool                             `json:"force,omitempty" doc:"Send although a reminder went to the same customer in the last 24 hours."`
	ExpectedAmount platform.Optional[platform.Dec4] `json:"expected_amount" doc:"The amount the user confirmed (the cash still expected: balance_due less the withholding not yet booked). When set and it differs from the current amount the send is 409 reminder_amount_changed, naming the current amount and currency; nothing is sent."`
}

type sendReminderIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
	Body      SendReminderRequest
}

type reminderBody struct{ Body Reminder }

type remindersBody struct {
	Body []Reminder
}

func (r reminderRoutes) send(ctx context.Context, in *sendReminderIn) (*reminderBody, error) {
	rin := ReminderIn{InvoiceID: in.InvoiceID, Force: in.Body.Force, Channel: ReminderChannelREST}
	if to := orDefault(in.Body.ToEmail, ""); to != "" {
		norm, ok := platform.NormalizeEmail(to)
		if !ok {
			return nil, platform.FieldError("to_email", "invalid email address: "+to)
		}
		rin.ToEmail = &norm
	}
	if uid := platform.PrincipalFrom(ctx).UserID; uid != uuid.Nil {
		rin.ActorUserID = &uid
	}
	var expected *decimal.Decimal
	if v := in.Body.ExpectedAmount.Merge(nil); v != nil {
		d := v.Decimal()
		expected = &d
	}
	now := r.clock.Now()
	var out Reminder
	// The transaction runs uncancellable so a hang-up after the email went
	// out cannot roll the log row back.
	err := db.InTx(context.WithoutCancel(ctx), r.pool, func(tx pgx.Tx) error {
		d, err := PrepareReminder(ctx, tx, rin, now)
		if err != nil {
			return err
		}
		// The amount the user confirmed binds the send: a payment since the
		// dialog opened makes it stale.
		if expected != nil && !expected.Equal(d.Amount) {
			return &AmountChangedError{Amount: d.Amount, Currency: d.Invoice.Currency}
		}
		// Past this point the send is not abandoned on a hang-up: an email
		// delivered without its log row would slip the 24 h guard.
		out, err = SendReminder(context.WithoutCancel(ctx), tx, r.mail, d, rin, now)
		return err
	})
	if err != nil {
		return nil, reminderError(err)
	}
	return &reminderBody{Body: out}, nil
}

func (r reminderRoutes) list(ctx context.Context, in *idIn) (*remindersBody, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM invoices WHERE invoice_id = $1)`, in.InvoiceID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, notFound()
	}
	out, err := ListReminders(ctx, r.pool, in.InvoiceID)
	if err != nil {
		return nil, err
	}
	return &remindersBody{Body: out}, nil
}

// AmountChangedError refuses a REST send whose expected_amount is not the
// cash still expected now (Amount, in Currency).
type AmountChangedError struct {
	Amount   decimal.Decimal
	Currency string
}

// AmountText is the current amount as the email prints it.
func (e *AmountChangedError) AmountText() string { return platform.PyDecimalStr(e.Amount) }

func (e *AmountChangedError) Error() string {
	return fmt.Sprintf("the amount due is now %s %s, not the amount you confirmed; review it and send again",
		e.AmountText(), e.Currency)
}

// reminderError is err as the client reads it: the amount-changed 409 (with
// the current amount and currency as details), the repeat guard 409 (with
// the earlier invoice_number and sent_at as details), an unknown invoice 404,
// a failed send 502, every other refusal 422.
func reminderError(err error) error {
	var changed *AmountChangedError
	if errors.As(err, &changed) {
		return apierr.New(http.StatusConflict, "reminder_amount_changed", i18n.T(err.Error()),
			apierr.Detail{Field: "amount", Message: i18n.T(changed.AmountText())},
			apierr.Detail{Field: "currency", Message: i18n.T(changed.Currency)})
	}
	var recent *RecentReminderError
	if errors.As(err, &recent) {
		details := []apierr.Detail{{Field: "sent_at", Message: i18n.T(recent.SentAt.UTC().Format(time.RFC3339))}}
		if recent.InvoiceNumber != nil {
			details = append([]apierr.Detail{{Field: "invoice_number", Message: i18n.T(*recent.InvoiceNumber)}}, details...)
		}
		return apierr.New(http.StatusConflict, "reminder_recent", i18n.T(err.Error()), details...)
	}
	if errors.Is(err, ErrInvoicing) {
		status := http.StatusUnprocessableEntity
		switch RefusalCode(err) {
		case "invoice_not_found":
			status = http.StatusNotFound
		case "send_failed":
			status = http.StatusBadGateway
		}
		return platform.Err(status, RefusalCode(err), err.Error())
	}
	return err
}
