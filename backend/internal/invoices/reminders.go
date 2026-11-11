package invoices

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LOQATO/gokit/observe"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// Payment reminders (UI revamp part 2, "Send reminder"). The REST route and
// the MCP tool send_payment_reminder share these functions, so the refusal
// rules, the email and the 24 h repeat guard live in one place. The refusal
// sentences and the email text are the MCP tool's (tools_gated.go before
// this change).

// Reminder channels.
const (
	ReminderChannelREST = "REST"
	ReminderChannelMCP  = "MCP"
)

// ReminderGuardWindow is how long a reminder to a customer holds back
// another one to the same customer, unless forced.
const ReminderGuardWindow = 24 * time.Hour

// The refusal sentences, as the MCP tool answered them.
const (
	errReminderNoRecipient = "no recipient: pass to_email or set customer contact_email"
	errReminderSendFailed  = "failed to send payment reminder"
)

// ReminderIn is a reminder request: the invoice, an optional recipient
// (else the customer's contact email), whether to override the repeat
// guard, the channel ("REST" or "MCP") and the user who asked, when known.
type ReminderIn struct {
	InvoiceID   uuid.UUID
	ToEmail     *string
	Force       bool
	Channel     string
	ActorUserID *uuid.UUID
}

// Reminder is a logged reminder.
type Reminder struct {
	ReminderID   uuid.UUID     `json:"reminder_id"`
	InvoiceID    uuid.UUID     `json:"invoice_id"`
	SentAt       platform.Time `json:"sent_at"`
	SentByUserID *uuid.UUID    `json:"sent_by_user_id"`
	Channel      string        `json:"channel"`
	Recipient    string        `json:"recipient"`
	Amount       platform.Dec4 `json:"amount" doc:"The cash the reminder asked for: balance_due less the withholding not yet booked."`
	Currency     string        `json:"currency"`
}

// ReminderDraft is a validated reminder, ready to send: the invoice (locked
// FOR UPDATE by PrepareReminder), the recipient and the cash still expected.
type ReminderDraft struct {
	Invoice   invoicesdb.Invoice
	Recipient string
	Amount    decimal.Decimal
}

// AmountText is the amount as the email and the MCP preview print it.
func (d ReminderDraft) AmountText() string { return platform.PyDecimalStr(d.Amount) }

// RecentReminderError is the repeat guard's refusal: a reminder went to the
// same customer (or, for an invoice with no customer, for the same invoice)
// within ReminderGuardWindow. It is an ErrInvoicing.
type RecentReminderError struct {
	InvoiceID     uuid.UUID
	InvoiceNumber *string
	SentAt        time.Time
}

func (e *RecentReminderError) Error() string {
	ref := e.InvoiceID.String()
	if e.InvoiceNumber != nil && *e.InvoiceNumber != "" {
		ref = *e.InvoiceNumber
	}
	return fmt.Sprintf("a reminder for invoice %s was sent at %s, less than 24 hours ago; pass force to send another",
		ref, e.SentAt.UTC().Format(time.RFC3339))
}

// Is makes the guard's refusal an ErrInvoicing.
func (e *RecentReminderError) Is(target error) bool { return target == ErrInvoicing }

// PrepareReminder reads the invoice FOR UPDATE and validates it: VOID and
// PAID are refused, then a recurring template (a DRAFT too), then a DRAFT.
// It resolves the recipient and the cash still expected, and applies the
// repeat guard unless in.Force. Every refusal is an ErrInvoicing.
func PrepareReminder(ctx context.Context, tx pgx.Tx, in ReminderIn, now time.Time) (ReminderDraft, error) {
	q := invoicesdb.New(tx)
	inv, err := q.GetInvoiceForUpdate(ctx, in.InvoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReminderDraft{}, refuse("invoice_not_found", "invoice not found")
	}
	if err != nil {
		return ReminderDraft{}, err
	}
	if inv.Status == "VOID" || inv.Status == "PAID" {
		return ReminderDraft{}, refuse("invoice_not_remindable", "cannot remind on a %s invoice", inv.Status)
	}
	// As for sending the invoice (PORT-NOTES 112): nothing goes out for an
	// unfinalized invoice. A template is checked first, as it is a DRAFT too.
	if inv.IsTemplate {
		return ReminderDraft{}, refuse("invoice_template", "cannot send a recurring template")
	}
	if inv.Status == "DRAFT" {
		return ReminderDraft{}, refuse("invoice_not_finalized", "finalize the invoice before sending it")
	}
	// Serialise reminders to one customer (the invoice itself when it has no
	// customer), forced or not, so two concurrent sends cannot both pass the
	// guard. An advisory lock, not the customers row: its FK KEY SHARE locks
	// from invoice writes would conflict.
	lockKey := inv.InvoiceID.String()
	if inv.CustomerID.Valid {
		lockKey = uuid.UUID(inv.CustomerID.Bytes).String()
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('invoice_reminder:' || $1::text, 0))`, lockKey); err != nil {
		return ReminderDraft{}, err
	}
	recipient, err := DefaultRecipient(ctx, tx, inv, in.ToEmail)
	if err != nil {
		return ReminderDraft{}, err
	}
	d := ReminderDraft{
		Invoice: inv, Recipient: recipient,
		Amount: withholding.ExpectedCash(inv.BalanceDue, inv.WhtAmount, inv.WhtBooked),
	}
	if !in.Force {
		if err := CheckReminderGuard(ctx, tx, d, now); err != nil {
			return ReminderDraft{}, err
		}
	}
	return d, nil
}

// DefaultRecipient is Python's `to_email or customer.contact_email`, or the
// no-recipient refusal when neither is set.
func DefaultRecipient(ctx context.Context, tx pgx.Tx, inv invoicesdb.Invoice, toEmail *string) (string, error) {
	if toEmail != nil && *toEmail != "" {
		return *toEmail, nil
	}
	if inv.CustomerID.Valid {
		contact, err := invoicesdb.New(tx).GetCustomerContactEmail(ctx, inv.CustomerID.Bytes)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if err == nil && contact != nil && *contact != "" {
			return *contact, nil
		}
	}
	return "", refuse("no_recipient", errReminderNoRecipient)
}

// CheckReminderGuard is the repeat guard: a *RecentReminderError when a
// reminder went to d's customer within ReminderGuardWindow before now. An
// invoice with no customer counts only its own reminders. PrepareReminder
// runs it unless forced; a caller that validated with Force (the MCP
// confirm, which spends its token first) runs it itself.
func CheckReminderGuard(ctx context.Context, tx pgx.Tx, d ReminderDraft, now time.Time) error {
	row, err := invoicesdb.New(tx).GetRecentReminder(ctx, invoicesdb.GetRecentReminderParams{
		Since: now.Add(-ReminderGuardWindow), CustomerID: d.Invoice.CustomerID, InvoiceID: d.Invoice.InvoiceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return &RecentReminderError{InvoiceID: row.InvoiceID, InvoiceNumber: row.InvoiceNumber, SentAt: row.SentAt}
}

// SendReminder emails the reminder d describes and then logs it, in tx, at
// now. A send the provider refuses is the refusal "failed to send payment
// reminder" and logs nothing.
func SendReminder(ctx context.Context, tx pgx.Tx, mail email.Sender, d ReminderDraft, in ReminderIn, now time.Time) (Reminder, error) {
	inv := d.Invoice
	id := inv.InvoiceID
	cash := d.AmountText()
	subject, ref := "Payment reminder", id.String()
	if inv.InvoiceNumber != nil && *inv.InvoiceNumber != "" {
		subject, ref = "Payment reminder: invoice "+*inv.InvoiceNumber, *inv.InvoiceNumber
	}
	msg := email.Message{
		To: d.Recipient, Subject: subject,
		Text: "This is a reminder that invoice " + ref + " has an outstanding balance of " + cash + " " + inv.Currency + ".",
	}
	msg.IdempotencyKey = email.IdempotencyKey(id.String(), msg, now)
	if err := mail.Send(ctx, msg); err != nil {
		observe.LoggerFrom(ctx).ErrorContext(ctx, "invoices: payment reminder send failed", "invoice_id", id, "channel", in.Channel, "err", err)
		return Reminder{}, refuse("send_failed", errReminderSendFailed)
	}
	var actor pgtype.UUID
	if in.ActorUserID != nil {
		actor = pgtype.UUID{Bytes: *in.ActorUserID, Valid: true}
	}
	row, err := invoicesdb.New(tx).InsertInvoiceReminder(ctx, invoicesdb.InsertInvoiceReminderParams{
		ReminderID: uuid.New(), InvoiceID: id, SentAt: now, SentByUserID: actor,
		Channel: in.Channel, Recipient: d.Recipient, Amount: d.Amount, Currency: inv.Currency,
	})
	if err != nil {
		return Reminder{}, err
	}
	return reminderOf(row), nil
}

// ListReminders is the invoice's reminder history, latest first.
func ListReminders(ctx context.Context, db invoicesdb.DBTX, invoiceID uuid.UUID) ([]Reminder, error) {
	rows, err := invoicesdb.New(db).ListInvoiceReminders(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	out := make([]Reminder, 0, len(rows))
	for _, r := range rows {
		out = append(out, reminderOf(r))
	}
	return out, nil
}

// LastReminderByCustomer is each given customer's latest reminder time; a
// customer never reminded is absent.
func LastReminderByCustomer(ctx context.Context, db invoicesdb.DBTX, customerIDs []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(customerIDs) == 0 {
		return out, nil
	}
	rows, err := invoicesdb.New(db).ListLastReminderByCustomer(ctx, customerIDs)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.CustomerID] = r.SentAt
	}
	return out, nil
}

func reminderOf(r invoicesdb.InvoiceReminder) Reminder {
	var by *uuid.UUID
	if r.SentByUserID.Valid {
		u := uuid.UUID(r.SentByUserID.Bytes)
		by = &u
	}
	return Reminder{
		ReminderID: r.ReminderID, InvoiceID: r.InvoiceID, SentAt: platform.Time{Time: r.SentAt.UTC()},
		SentByUserID: by, Channel: r.Channel, Recipient: r.Recipient,
		Amount: platform.NewDec4(r.Amount), Currency: r.Currency,
	}
}
