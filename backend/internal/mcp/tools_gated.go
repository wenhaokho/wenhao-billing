package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth/oauthdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/pdf"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// The 5 gated tools (inventory §2.6 rows 38-42). Each runs in two calls: the
// first (no confirm_token) does the lookups and refusals and answers a
// preview with a confirm token, writing nothing; the second recomputes the
// payload from the row as it is now (read FOR UPDATE, so it cannot move
// before the action), verifies the token against it, records the token as
// spent (Go change 4: single use) and acts. Everything the second call does
// is in the helper's transaction, so a refusal after the spend (a non-DRAFT
// delete, a failed send) rolls the spend back too and the token stays usable
// until it expires.

// Python's whole docstrings, as FastMCP sends them.
const (
	descDeleteInvoice  = "Permanently delete a DRAFT invoice. GATED: first call (no\nconfirm_token) previews the delete and returns a confirm_token; call\nagain passing that confirm_token to actually delete. Only DRAFT\ninvoices can be deleted — invoicing.delete_invoice refuses anything\nelse."
	descDeleteBill     = "Permanently delete a DRAFT bill. GATED: first call (no\nconfirm_token) previews the delete and returns a confirm_token; call\nagain passing that confirm_token to actually delete. Only DRAFT bills\ncan be deleted — a bill that has moved past DRAFT already affects AP\nand should be voided instead (see void_bill)."
	descDeleteCustomer = "Deactivate a customer (soft delete — sets active=False). GATED:\nfirst call (no confirm_token) previews the action and returns a\nconfirm_token; call again passing that confirm_token to actually\ndeactivate. Matches the existing DELETE /customers/{id} endpoint:\ncustomers are never hard-deleted anywhere in this app."
	descSendInvoice    = "Email an invoice PDF to a customer. GATED: first call (no\nconfirm_token) previews the send and returns a confirm_token; call again\npassing that confirm_token to actually send. If invoice status is DRAFT\nit is promoted to SENT once the email is sent."
	descSendReminder   = "Email a payment reminder for an outstanding invoice. GATED: first\ncall (no confirm_token) previews the send and returns a confirm_token;\ncall again passing that confirm_token to actually send. Bound to the\ninvoice's cash still expected (balance_due less unbooked PPh) so a partial\npayment between preview and confirm invalidates a stale token. No PDF\nis attached — a plain-text/HTML reminder only."
)

const (
	actDeleteInvoice  = "delete_invoice"
	actDeleteBill     = "delete_bill"
	actDeleteCustomer = "delete_customer"
	actSendInvoice    = "send_invoice_email"
	actSendReminder   = "send_payment_reminder"

	// errConfirmSpent answers a second use of a confirm token (Go change 4).
	errConfirmSpent = "confirm_token already used — re-request the action"
	// errSendFailed answers a send the mail provider refused; nothing changed.
	errSendFailed  = "failed to send invoice email"
	errNoRecipient = "no recipient: pass to_email or set customer contact_email"

	// spentConfirmGrace keeps a spent row this long past its token's expiry,
	// for clock skew between replicas (as spent codes are kept).
	spentConfirmGrace = 5 * time.Minute
)

type (
	deleteInvoiceIn struct {
		InvoiceID    string  `json:"invoice_id"`
		ConfirmToken *string `json:"confirm_token"`
	}
	deleteBillIn struct {
		BillID       string  `json:"bill_id"`
		ConfirmToken *string `json:"confirm_token"`
	}
	deleteCustomerIn struct {
		CustomerID   string  `json:"customer_id"`
		ConfirmToken *string `json:"confirm_token"`
	}
	sendInvoiceIn struct {
		InvoiceID    string  `json:"invoice_id"`
		ToEmail      *string `json:"to_email"`
		Subject      *string `json:"subject"`
		Message      *string `json:"message"`
		ConfirmToken *string `json:"confirm_token"`
	}
	sendReminderIn struct {
		InvoiceID    string  `json:"invoice_id"`
		ToEmail      *string `json:"to_email"`
		ConfirmToken *string `json:"confirm_token"`
	}
)

// object builds an Object from key, value pairs, in order.
func object(kv ...any) (Object, error) {
	var o Object
	for i := 1; i < len(kv); i += 2 {
		key, _ := kv[i-1].(string)
		if err := o.set(key, kv[i]); err != nil {
			return Object{}, err
		}
	}
	return o, nil
}

// text is *s, or "" for nil.
func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// gate holds what the gated tools share: the confirm-token key, the clock,
// the mail sender, and the pool the spent-token prune runs on.
type gate struct {
	pool    *pgxpool.Pool
	confirm *Confirmer
	clock   platform.Clock
	mail    email.Sender
}

// spend verifies token for the action and payload as they are now, then
// records it as spent in tx. Expired, malformed or mismatched tokens are
// refused with Python's sentences; a token already spent is refused with
// errConfirmSpent. Spent rows past their expiry (and the grace) are pruned.
func (g gate) spend(ctx context.Context, tx pgx.Tx, token, action string, payload map[string]string) error {
	now := g.clock.Now()
	if err := g.confirm.VerifyConfirm(token, action, payload, now); err != nil {
		return Refuse("%s", err.Error())
	}
	key, expires, err := spentKey(token)
	if err != nil {
		return Refuse("%s", err.Error())
	}
	// The prune runs on the pool, outside the action's transaction: inside
	// it, the deleted rows would stay locked until that transaction ends
	// (after a slow send), and every other confirm's prune would wait on them.
	if err := oauthdb.New(g.pool).PruneSpentConfirmTokens(ctx, now.Add(-spentConfirmGrace)); err != nil {
		return err
	}
	q := oauthdb.New(tx)
	if _, err := q.SpendConfirmToken(ctx, oauthdb.SpendConfirmTokenParams{TokenHash: key, ExpiresAt: expires}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Refuse(errConfirmSpent)
		}
		return err
	}
	return nil
}

// targetPayload is the delete tools' payload (Python's _payload).
func targetPayload(id uuid.UUID) map[string]string {
	return map[string]string{"target_id": id.String()}
}

// invoiceFor reads the invoice id names: FOR UPDATE on a confirming call,
// so the payload it recomputes holds until the action is done. An unknown
// one is Python's "invoice not found".
func invoiceFor(ctx context.Context, tx pgx.Tx, rawID string, confirming bool) (invoicesdb.Invoice, error) {
	id, err := parseID("invoice_id", rawID)
	if err != nil {
		return invoicesdb.Invoice{}, refuseArg(err)
	}
	q := invoicesdb.New(tx)
	var inv invoicesdb.Invoice
	if confirming {
		inv, err = q.GetInvoiceForUpdate(ctx, id)
	} else {
		inv, err = q.GetInvoice(ctx, id)
	}
	if noRows(err) {
		return invoicesdb.Invoice{}, Refuse("invoice not found")
	}
	return inv, err
}

// recipientFor is Python's `to_email or customer.contact_email`, or the
// refusal when neither is set.
func recipientFor(ctx context.Context, tx pgx.Tx, inv invoicesdb.Invoice, toEmail *string) (string, error) {
	if to := text(toEmail); to != "" {
		return to, nil
	}
	if inv.CustomerID.Valid {
		c, err := customersdb.New(tx).GetCustomer(ctx, inv.CustomerID.Bytes)
		if err != nil && !noRows(err) {
			return "", err
		}
		if err == nil && text(c.ContactEmail) != "" {
			return *c.ContactEmail, nil
		}
	}
	return "", Refuse(errNoRecipient)
}

// registerGatedTools adds the 5 gated tools, in Python's tools/list order.
func registerGatedTools(s *mcp.Server, d Deps) {
	clock := d.Clock
	if clock == nil {
		clock = platform.SystemClock{}
	}
	mail := d.Sender
	if mail == nil {
		mail = email.LogSender{}
	}
	g := gate{pool: d.Pool, confirm: NewConfirmer(d.Cfg.MCPConfirmSecret), clock: clock, mail: mail}
	gatedInput := func(idName string, more ...prop) string {
		return toolInput(append(append([]prop{p(idName, tStr)}, more...), p("confirm_token", tNStr))...)
	}

	addTool(s, d, writeTool(actDeleteInvoice, descDeleteInvoice, gatedInput("invoice_id")),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in deleteInvoiceIn) (Object, error) {
			inv, err := invoiceFor(ctx, tx, in.InvoiceID, in.ConfirmToken != nil)
			if err != nil {
				return Object{}, err
			}
			id := inv.InvoiceID
			payload := targetPayload(id)
			if in.ConfirmToken == nil {
				return object("requires_confirmation", true, "action", actDeleteInvoice, "invoice_id", id.String(),
					"invoice_number", inv.InvoiceNumber, "status", inv.Status,
					"confirm_token", g.confirm.MakeConfirm(actDeleteInvoice, payload, g.clock.Now()))
			}
			if err := g.spend(ctx, tx, *in.ConfirmToken, actDeleteInvoice, payload); err != nil {
				return Object{}, err
			}
			if err := invoices.DeleteInvoice(ctx, tx, id); err != nil {
				return Object{}, refuseDomain(err)
			}
			return object("deleted", true, "invoice_id", id.String())
		})

	addTool(s, d, writeTool(actDeleteBill, descDeleteBill, gatedInput("bill_id")),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in deleteBillIn) (Object, error) {
			id, err := parseID("bill_id", in.BillID)
			if err != nil {
				return Object{}, refuseArg(err)
			}
			q := billsdb.New(tx)
			var bill billsdb.Bill
			if in.ConfirmToken != nil {
				bill, err = q.GetBillForUpdate(ctx, id)
			} else {
				bill, err = q.GetBill(ctx, id)
			}
			if noRows(err) {
				return Object{}, Refuse("bill not found")
			}
			if err != nil {
				return Object{}, err
			}
			payload := targetPayload(id)
			if in.ConfirmToken == nil {
				return object("requires_confirmation", true, "action", actDeleteBill, "bill_id", id.String(),
					"bill_number", bill.BillNumber, "status", bill.Status,
					"confirm_token", g.confirm.MakeConfirm(actDeleteBill, payload, g.clock.Now()))
			}
			if err := g.spend(ctx, tx, *in.ConfirmToken, actDeleteBill, payload); err != nil {
				return Object{}, err
			}
			// The DRAFT-only delete: any other status is its refusal.
			if err := bills.DeleteBill(ctx, tx, id); err != nil {
				if noRows(err) {
					return Object{}, Refuse("bill not found")
				}
				return Object{}, refuseDomain(err)
			}
			return object("deleted", true, "bill_id", id.String())
		})

	addTool(s, d, writeTool(actDeleteCustomer, descDeleteCustomer, gatedInput("customer_id")),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in deleteCustomerIn) (Object, error) {
			id, err := parseID("customer_id", in.CustomerID)
			if err != nil {
				return Object{}, refuseArg(err)
			}
			q := customersdb.New(tx)
			var c customersdb.Customer
			if in.ConfirmToken != nil {
				c, err = q.GetCustomerForUpdate(ctx, id)
			} else {
				c, err = q.GetCustomer(ctx, id)
			}
			if noRows(err) {
				return Object{}, Refuse("customer not found")
			}
			if err != nil {
				return Object{}, err
			}
			payload := targetPayload(id)
			if in.ConfirmToken == nil {
				return object("requires_confirmation", true, "action", actDeleteCustomer, "customer_id", id.String(),
					"name", c.Name, "confirm_token", g.confirm.MakeConfirm(actDeleteCustomer, payload, g.clock.Now()))
			}
			if err := g.spend(ctx, tx, *in.ConfirmToken, actDeleteCustomer, payload); err != nil {
				return Object{}, err
			}
			// The DELETE /customers/{id} route's soft delete; it never refuses.
			if _, err := q.DeactivateCustomer(ctx, id); err != nil {
				return Object{}, err
			}
			return object("deleted", true, "customer_id", id.String())
		})

	addTool(s, d, writeTool(actSendInvoice, descSendInvoice, toolInput(
		p("invoice_id", tStr), p("to_email", tNStr), p("subject", tNStr), p("message", tNStr), p("confirm_token", tNStr))),
		g.sendInvoiceEmail)

	addTool(s, d, writeTool(actSendReminder, descSendReminder, toolInput(
		p("invoice_id", tStr), p("to_email", tNStr), p("confirm_token", tNStr))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in sendReminderIn) (Object, error) {
			inv, err := invoiceFor(ctx, tx, in.InvoiceID, in.ConfirmToken != nil)
			if err != nil {
				return Object{}, err
			}
			if inv.Status == "VOID" || inv.Status == "PAID" {
				return Object{}, Refuse("cannot remind on a %s invoice", inv.Status)
			}
			recipient, err := recipientFor(ctx, tx, inv, in.ToEmail)
			if err != nil {
				return Object{}, err
			}
			id := inv.InvoiceID
			// The cash still expected, so a partial payment since the preview
			// makes the token stale.
			cash := platform.PyDecimalStr(withholding.ExpectedCash(inv.BalanceDue, inv.WhtAmount, inv.WhtBooked))
			payload := map[string]string{"target_id": id.String(), "amount": cash, "currency": inv.Currency, "recipient": recipient}
			if in.ConfirmToken == nil {
				return object("requires_confirmation", true, "action", actSendReminder, "invoice_id", id.String(),
					"invoice_number", inv.InvoiceNumber, "recipient", recipient, "amount", cash, "currency", inv.Currency,
					"confirm_token", g.confirm.MakeConfirm(actSendReminder, payload, g.clock.Now()))
			}
			if err := g.spend(ctx, tx, *in.ConfirmToken, actSendReminder, payload); err != nil {
				return Object{}, err
			}
			number := text(inv.InvoiceNumber)
			subject, ref := "Payment reminder", id.String()
			if number != "" {
				subject, ref = "Payment reminder: invoice "+number, number
			}
			msg := email.Message{
				To: recipient, Subject: subject,
				Text: "This is a reminder that invoice " + ref + " has an outstanding balance of " + cash + " " + inv.Currency + ".",
			}
			msg.IdempotencyKey = email.IdempotencyKey(id.String(), msg, g.clock.Now())
			if err := g.mail.Send(ctx, msg); err != nil {
				logger(ctx).ErrorContext(ctx, "mcp send_payment_reminder: send failed", "invoice_id", id, "err", err)
				return Object{}, Refuse("failed to send payment reminder")
			}
			// The reminder is out and nothing else runs here; runTool commits
			// the spent token without the request's cancellation, so a client
			// that hangs up now cannot make the token replayable.
			return object("sent", true, "invoice_id", id.String(), "recipient", recipient)
		})
}

// sendInvoiceEmail is send_invoice_email's body.
func (g gate) sendInvoiceEmail(ctx context.Context, tx pgx.Tx, _ principalRef, in sendInvoiceIn) (Object, error) {
	inv, err := invoiceFor(ctx, tx, in.InvoiceID, in.ConfirmToken != nil)
	if err != nil {
		return Object{}, err
	}
	if inv.Status == "VOID" {
		return Object{}, Refuse("cannot send a voided invoice")
	}
	recipient, err := recipientFor(ctx, tx, inv, in.ToEmail)
	if err != nil {
		return Object{}, err
	}
	id := inv.InvoiceID
	amount := platform.PyDecimalStr(inv.Amount)
	// Go change 4: the subject and the message are in the MAC too, so
	// the confirmed email is the previewed one. Null and "" are the
	// same here, as both mean the default.
	payload := map[string]string{
		"target_id": id.String(), "amount": amount, "currency": inv.Currency, "recipient": recipient,
		"subject": text(in.Subject), "message": text(in.Message),
	}
	if in.ConfirmToken == nil {
		return object("requires_confirmation", true, "action", actSendInvoice, "invoice_id", id.String(),
			"invoice_number", inv.InvoiceNumber, "recipient", recipient, "amount", amount, "currency", inv.Currency,
			"confirm_token", g.confirm.MakeConfirm(actSendInvoice, payload, g.clock.Now()))
	}
	if err := g.spend(ctx, tx, *in.ConfirmToken, actSendInvoice, payload); err != nil {
		return Object{}, err
	}

	// The send route's renderer, on the locked row in this transaction.
	data, err := pdf.LoadInvoice(ctx, tx, id)
	if err != nil {
		return Object{}, err
	}
	pdfBytes, err := pdf.RenderInvoice(ctx, pdf.InvoiceDocument(data))
	if err != nil {
		return Object{}, err
	}
	number := text(inv.InvoiceNumber)
	subject := "Invoice"
	filename := "invoice-" + id.String()[:8] + ".pdf"
	if number != "" {
		subject = "Invoice " + number
		filename = "invoice-" + number + ".pdf"
	}
	if s := text(in.Subject); s != "" {
		subject = s
	}
	body := "Please find attached invoice " + number + "."
	if m := text(in.Message); m != "" {
		body = m
	}
	msg := email.Message{
		To: recipient, Subject: subject, Text: body,
		Attachments: []email.Attachment{{Filename: filename, ContentType: "application/pdf", Content: pdfBytes}},
	}
	msg.IdempotencyKey = email.IdempotencyKey(id.String(), msg, g.clock.Now())
	// Sent before the status moves and inside the transaction: a
	// failed send rolls back the spent token and changes nothing, as
	// the send route answers 502 with the status unchanged.
	if err := g.mail.Send(ctx, msg); err != nil {
		logger(ctx).ErrorContext(ctx, "mcp send_invoice_email: send failed", "invoice_id", id, "err", err)
		return Object{}, Refuse(errSendFailed)
	}
	// The email is out, so the rest of the call (the status move here
	// and the helper's commit) no longer listens to the request: a
	// client that hangs up now must not roll back the status and the
	// spent token of an email the provider already accepted.
	ctx = context.WithoutCancel(ctx)
	// The email is out; a DRAFT or OPEN invoice becomes SENT.
	if err := invoicesdb.New(tx).MarkInvoiceSent(ctx, id); err != nil {
		return Object{}, err
	}
	return object("sent", true, "invoice_id", id.String(), "recipient", recipient)
}
