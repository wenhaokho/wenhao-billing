package invoices

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
		Description:   "Refuses a VOID or PAID invoice, a recurring template, a DRAFT and a send with no recipient (422). A reminder already sent to the same customer in the last 24 hours is 409 (the details name the earlier invoice_number and sent_at) unless force is true. A send failure is 502 and logs nothing.",
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
	ToEmail platform.Optional[string] `json:"to_email" doc:"The recipient; empty or null falls back to the customer's contact email."`
	Force   bool                      `json:"force,omitempty" doc:"Send although a reminder went to the same customer in the last 24 hours."`
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
	now := r.clock.Now()
	var out Reminder
	// The transaction runs uncancellable so a hang-up after the email went
	// out cannot roll the log row back.
	err := db.InTx(context.WithoutCancel(ctx), r.pool, func(tx pgx.Tx) error {
		d, err := PrepareReminder(ctx, tx, rin, now)
		if err != nil {
			return err
		}
		out, err = SendReminder(ctx, tx, r.mail, d, rin, now)
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

// reminderError is err as the client reads it: the repeat guard 409 (with
// the earlier invoice_number and sent_at as details), an unknown invoice 404,
// a failed send 502, every other refusal 422.
func reminderError(err error) error {
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
