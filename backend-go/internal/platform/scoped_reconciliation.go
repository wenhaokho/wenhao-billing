package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedpayment"
)

type reconciliationRejection string

func (e reconciliationRejection) Error() string { return string(e) }

const exactSettlementMemo = " [exact invoice relief]"

func (h scopedProfileRouter) reconcilePayment(reverse bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.admin(w, r)
		if !ok {
			return
		}
		rawID := chi.URLParam(r, "payment_id")
		id, ok := parsePathUUID(rawID)
		if !ok {
			detail := uuidErrorDetail(rawID)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "payment_id"}, "Input should be a valid UUID, " + detail, rawID, map[string]string{"error": detail}}})
			return
		}
		body, ok := readBodyWithSyntax(w, r)
		if !ok {
			return
		}
		if len(body) == 0 {
			paymentValidationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
			return
		}
		var raw any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&raw); err != nil {
			paymentValidationResponse(w, itemUpdateJSONError(body, err, true))
			return
		}
		obj, ok := raw.(map[string]any)
		if !ok {
			paymentValidationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", raw, nil}})
			return
		}
		field := "invoice_id"
		if reverse {
			field = "reason"
		}
		value, exists := obj[field]
		if !exists {
			paymentValidationResponse(w, []validationError{{"missing", []any{"body", field}, "Field required", obj, nil}})
			return
		}
		s, ok := value.(string)
		var invoiceID pgtype.UUID
		if !ok || (!reverse && (invoiceID.Scan(s) != nil || !invoiceID.Valid)) {
			typ, msg := "string_type", "Input should be a valid string"
			if !reverse {
				typ, msg = "uuid_type", "UUID input should be a string, bytes or UUID object"
				if ok {
					detail := uuidErrorDetail(s)
					paymentValidationResponse(w, []validationError{{"uuid_parsing", []any{"body", field}, "Input should be a valid UUID, " + detail, value, map[string]string{"error": detail}}})
					return
				}
			}
			paymentValidationResponse(w, []validationError{{typ, []any{"body", field}, msg, value, nil}})
			return
		}
		adjustment := "NONE"
		if v, exists := obj["adjustment_type"]; !reverse && exists {
			adjustment, ok = v.(string)
			if !ok {
				paymentValidationResponse(w, []validationError{{"string_type", []any{"body", "adjustment_type"}, "Input should be a valid string", v, nil}})
				return
			}
		}
		if !reverse {
			switch adjustment {
			case "NONE", "PARTIAL_PAY", "CREDIT_ON_ACCOUNT", "BANK_FEE":
				// These are Python audit labels. The posting uses only stored amounts.
			default:
				authError(w, 400, fmt.Sprintf("unknown adjustment_type %q", adjustment))
				return
			}
		}
		ctx := r.Context()
		tx, err := (candidateTransactions{db: h.pool, scope: h.scope}).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		defer tx.Rollback(context.Background())
		q := scopedInvoicePaymentQueries{scopedInvoiceWriteQueries{tx: tx, scope: h.scope}}
		role, err := q.lockAdminRole(ctx, actor.UserID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && role != "admin" {
			authError(w, 403, "admin required")
			return
		}
		var payment dbqueryv8.Payment
		if err == nil {
			payment, err = h.applyReconciliation(ctx, q, id, invoiceID, actor.UserID, adjustment, s, reverse)
		}
		var rejection reconciliationRejection
		if errors.As(err, &rejection) {
			authError(w, 400, rejection.Error())
			return
		}
		if err == nil {
			var out paymentOutput
			out, err = paymentJSON(legacyPayment(payment))
			if err == nil {
				err = tx.Commit(ctx)
				if err == nil {
					w.Header().Set("Content-Type", "application/json")
					writeSafeJSON(w, out)
					return
				}
			}
		}
		authError(w, 500, "internal server error")
	}
}

func (h scopedProfileRouter) applyReconciliation(ctx context.Context, q scopedInvoicePaymentQueries, id, invoiceID, actor pgtype.UUID, adjustment, reason string, reverse bool) (dbqueryv8.Payment, error) {
	payment, err := scopedpayment.Get(ctx, q.tx, q.scope, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return payment, reconciliationRejection(fmt.Sprintf("payment %s not found", id.String()))
	}
	if err != nil {
		return payment, err
	}
	if reverse {
		if payment.Status != "CLEARED" {
			return payment, reconciliationRejection(fmt.Sprintf("only CLEARED payments can be reversed (got %s)", payment.Status))
		}
		invoiceID = payment.InvoiceID
		if !invoiceID.Valid {
			return payment, reconciliationRejection("payment has no invoice to restore")
		}
	}
	// Lock the invoice before the payment, including when approval replaces
	// a suggested invoice. Only a pending payment may change its links.
	invoice, err := q.LockInvoice(ctx, invoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return payment, reconciliationRejection(fmt.Sprintf("invoice %s not found", invoiceID.String()))
	}
	if err != nil {
		return payment, err
	}
	var locked pgtype.UUID
	if err := q.tx.QueryRow(ctx, `SELECT payment_id FROM payments WHERE business_id=$1 AND payment_id=$2 FOR UPDATE`,
		q.scope.UUID(), id).Scan(&locked); err != nil {
		return payment, err
	}
	payment, err = scopedpayment.Get(ctx, q.tx, q.scope, id)
	if err != nil {
		return payment, err
	}
	if reverse {
		if payment.Status != "CLEARED" || payment.InvoiceID != invoiceID || payment.CustomerID != invoice.CustomerID {
			return payment, reconciliationRejection("payment is no longer cleared against this invoice")
		}
		err = reverseScopedPayment(ctx, q, payment, invoice, actor, reason)
	} else {
		if payment.Status != "PENDING_MANUAL_REVIEW" {
			return payment, reconciliationRejection(fmt.Sprintf("payment %s is %s", id.String(), payment.Status))
		}
		base := h.cfg.BaseCurrency
		if base == "" {
			base = "IDR"
		}
		err = approveScopedPayment(ctx, q, payment, invoice, actor, adjustment, base)
	}
	if err != nil {
		return payment, err
	}
	return scopedpayment.Get(ctx, q.tx, q.scope, id)
}

func approveScopedPayment(ctx context.Context, q scopedInvoicePaymentQueries, p dbqueryv8.Payment, i dbquery.Invoice, actor pgtype.UUID, adjustment, base string) error {
	switch i.Status {
	case "OPEN", "SENT", "PARTIAL":
	default:
		return reconciliationRejection("invoice cannot accept a ledger payment")
	}
	if i.IsTemplate {
		return reconciliationRejection("invoice cannot accept a ledger payment")
	}
	amount, err := updateNumeric(p.Amount)
	if err != nil {
		return err
	}
	balance, err := updateNumeric(i.BalanceDue)
	if err != nil {
		return err
	}
	total, err := updateNumeric(i.Amount)
	if err != nil {
		return err
	}
	wht, err := updateNumeric(i.WhtAmount)
	if err != nil {
		return err
	}
	booked, err := updateNumeric(i.WhtBooked)
	if err != nil {
		return err
	}
	paymentWHT, err := updateNumeric(p.WhtAmount)
	if err != nil {
		return err
	}
	outstanding := wht.Sub(booked)
	if !paymentWHT.IsZero() || !amount.IsPositive() || !balance.IsPositive() || balance.GreaterThan(total) || outstanding.IsNegative() || booked.IsNegative() {
		return reconciliationRejection("invalid payment amount, invoice balance or withholding")
	}
	withheld := decimal.Zero
	same := p.Currency == i.Currency
	if !same || amount.Equal(balance.Sub(outstanding)) {
		withheld = outstanding
	}
	if withheld.GreaterThan(balance) || same && amount.Add(withheld).GreaterThan(balance) {
		return reconciliationRejection("payment exceeds invoice balance")
	}
	biz := q.scope.UUID()
	_, err = q.tx.Exec(ctx, `UPDATE payments SET invoice_id=$3, customer_id=$4, status='CLEARED', adjustment_type=$5, wht_amount=$6
		WHERE business_id=$1 AND payment_id=$2`, biz, p.PaymentID, i.InvoiceID, i.CustomerID, adjustment, invoiceNumeric(withheld))
	if err != nil {
		return err
	}
	entry, err := scopedledger.PostPayment(ctx, q.tx, q.scope, p.PaymentID, i.InvoiceID, base)
	if err != nil {
		return err
	}
	// Historical Python same-currency postings may have clamped the invoice
	// balance. This marker records that this posting relieved exactly its AR.
	if same {
		if _, err := q.tx.Exec(ctx, `UPDATE journal_entries SET memo=memo || $3 WHERE business_id=$1 AND entry_id=$2`, biz, entry, exactSettlementMemo); err != nil {
			return err
		}
		balance = balance.Sub(amount.Add(withheld))
	} else {
		balance = decimal.Zero
	}
	status := "PARTIAL"
	if balance.IsZero() {
		status = "PAID"
	}
	if err := q.ApplyInvoicePayment(ctx, dbquery.ApplyInvoicePaymentParams{InvoiceID: i.InvoiceID, BalanceDue: invoiceNumeric(balance), WhtBooked: invoiceNumeric(booked.Add(withheld)), Status: status}); err != nil {
		return err
	}
	if status == "PAID" {
		if err := q.queueHostingRestore(ctx, i.InvoiceID); err != nil {
			return err
		}
	}
	return scopedReconciliationLog(ctx, q, p.PaymentID, actor, "OVERRIDDEN", "manual approval against invoice "+i.InvoiceID.String())
}

func reverseScopedPayment(ctx context.Context, q scopedInvoicePaymentQueries, p dbqueryv8.Payment, i dbquery.Invoice, actor pgtype.UUID, reason string) error {
	switch i.Status {
	case "OPEN", "SENT", "PARTIAL", "PAID":
	default:
		return reconciliationRejection("invoice cannot be reopened by a payment reversal")
	}
	if i.IsTemplate {
		return reconciliationRejection("invoice cannot be reopened by a payment reversal")
	}
	biz := q.scope.UUID()
	rows, err := q.tx.Query(ctx, `SELECT entry_id,memo FROM journal_entries
		WHERE business_id=$1 AND source_type='PAYMENT' AND source_id=$2 ORDER BY entry_id FOR UPDATE`, biz, p.PaymentID)
	if err != nil {
		return err
	}
	type origin struct {
		id   pgtype.UUID
		memo pgtype.Text
	}
	entries, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (origin, error) {
		var e origin
		err := r.Scan(&e.id, &e.memo)
		return e, err
	})
	if err != nil {
		return err
	}
	expected := fmt.Sprintf("Payment %s cleared against invoice %s", p.PaymentID.String(), i.InvoiceID.String())
	if p.Currency == i.Currency {
		expected += exactSettlementMemo
	}
	if len(entries) != 1 || !entries[0].memo.Valid || entries[0].memo.String != expected {
		return reconciliationRejection("payment lacks a unique original journal with exact invoice relief")
	}
	entry := entries[0].id
	var reversed bool
	if err := q.tx.QueryRow(ctx, `SELECT reversed_by_entry_id IS NOT NULL FROM journal_entries WHERE business_id=$1 AND entry_id=$2`, biz, entry).Scan(&reversed); err != nil {
		return err
	}
	if reversed {
		return reconciliationRejection("payment journal was already reversed")
	}
	relief, err := scopedPaymentJournalRelief(ctx, q, p, i.Currency, entry)
	if err != nil {
		return err
	}
	balance, err := updateNumeric(i.BalanceDue)
	if err != nil {
		return err
	}
	total, err := updateNumeric(i.Amount)
	if err != nil {
		return err
	}
	booked, err := updateNumeric(i.WhtBooked)
	if err != nil {
		return err
	}
	withheld, err := updateNumeric(p.WhtAmount)
	if err != nil {
		return err
	}
	if balance.IsNegative() || balance.Add(relief).GreaterThan(total) || withheld.IsNegative() || booked.LessThan(withheld) {
		return reconciliationRejection("payment reversal would violate invoice balance or withholding")
	}
	if err := lockReversalHosting(ctx, q, i.InvoiceID); err != nil {
		return err
	}
	if _, err := scopedledger.Reverse(ctx, q.tx, q.scope, entry, "Reversal: "+reason); err != nil {
		return err
	}
	if _, err := q.tx.Exec(ctx, `DELETE FROM hosting_restore_intents WHERE business_id=$1 AND invoice_id=$2`, biz, i.InvoiceID); err != nil {
		return err
	}
	if err := q.ApplyInvoicePayment(ctx, dbquery.ApplyInvoicePaymentParams{InvoiceID: i.InvoiceID, BalanceDue: invoiceNumeric(balance.Add(relief)), WhtBooked: invoiceNumeric(booked.Sub(withheld)), Status: "OPEN"}); err != nil {
		return err
	}
	if _, err := q.tx.Exec(ctx, `UPDATE payments SET status='FLAGGED' WHERE business_id=$1 AND payment_id=$2`, biz, p.PaymentID); err != nil {
		return err
	}
	return scopedReconciliationLog(ctx, q, p.PaymentID, actor, "REVERSED", reason)
}

// Hosting workers lock the item before changing an intent or operation lease.
func lockReversalHosting(ctx context.Context, q scopedInvoicePaymentQueries, invoice pgtype.UUID) error {
	biz := q.scope.UUID()
	rows, err := q.tx.Query(ctx, `SELECT i.item_id FROM items i
		WHERE i.business_id=$1 AND (
			EXISTS (SELECT 1 FROM invoice_line_items l WHERE l.business_id=i.business_id AND l.item_id=i.item_id AND l.invoice_id=$2)
			OR EXISTS (SELECT 1 FROM hosting_restore_intents d WHERE d.business_id=i.business_id AND d.item_id=i.item_id AND d.invoice_id=$2)
		) ORDER BY i.item_id FOR UPDATE`, biz, invoice)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowTo[pgtype.UUID])
	if err != nil {
		return err
	}
	var intentClaimed, operationUnresolved bool
	err = q.tx.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM hosting_restore_intents WHERE business_id=$1 AND invoice_id=$2 AND lease_token IS NOT NULL),
		EXISTS (SELECT 1 FROM hosting_operation_leases WHERE business_id=$1 AND item_id=ANY($3::uuid[]))`,
		biz, invoice, items).Scan(&intentClaimed, &operationUnresolved)
	if err != nil {
		return err
	}
	if intentClaimed || operationUnresolved {
		return reconciliationRejection("invoice has a claimed hosting restore intent or unresolved hosting operation")
	}
	return nil
}

func scopedPaymentJournalRelief(ctx context.Context, q scopedInvoicePaymentQueries, p dbqueryv8.Payment, invoiceCurrency string, entry pgtype.UUID) (decimal.Decimal, error) {
	rows, err := q.tx.Query(ctx, `SELECT a.code, l.currency, l.debit, l.credit
		FROM journal_lines l JOIN chart_of_accounts a ON a.business_id=l.business_id AND a.account_id=l.account_id
		WHERE l.business_id=$1 AND l.entry_id=$2 AND a.code IN ('1000','1100','1300')
		ORDER BY l.line_id FOR SHARE OF a,l`, q.scope.UUID(), entry)
	if err != nil {
		return decimal.Zero, err
	}
	defer rows.Close()
	native := make(map[string]decimal.Decimal)
	reject := reconciliationRejection("payment journal does not prove invoice relief")
	for rows.Next() {
		var code, currency string
		var debit, credit pgtype.Numeric
		if err := rows.Scan(&code, &currency, &debit, &credit); err != nil {
			return decimal.Zero, err
		}
		if _, exists := native[code]; exists {
			return decimal.Zero, reject
		}
		d, err := updateNumeric(debit)
		if err != nil {
			return decimal.Zero, err
		}
		c, err := updateNumeric(credit)
		if err != nil {
			return decimal.Zero, err
		}
		switch code {
		case "1100":
			if currency != invoiceCurrency || !d.IsZero() || !c.IsPositive() {
				return decimal.Zero, reject
			}
			native[code] = c
		case "1000", "1300":
			wantCurrency := invoiceCurrency
			if code == "1000" {
				wantCurrency = p.Currency
			}
			if currency != wantCurrency || !c.IsZero() || !d.IsPositive() {
				return decimal.Zero, reject
			}
			native[code] = d
		}
	}
	if err := rows.Err(); err != nil {
		return decimal.Zero, err
	}
	amount, err := updateNumeric(p.Amount)
	if err != nil {
		return decimal.Zero, err
	}
	withheld, err := updateNumeric(p.WhtAmount)
	if err != nil {
		return decimal.Zero, err
	}
	relief := native["1100"]
	if !relief.IsPositive() || !native["1000"].Equal(amount) || !native["1300"].Equal(withheld) ||
		(p.Currency == invoiceCurrency && !relief.Equal(amount.Add(withheld))) {
		return decimal.Zero, reject
	}
	return relief, nil
}

func scopedReconciliationLog(ctx context.Context, q scopedInvoicePaymentQueries, payment, actor pgtype.UUID, action, reason string) error {
	_, err := q.tx.Exec(ctx, `INSERT INTO reconciliation_log(business_id,log_id,payment_id,action,reasons,actor_user_id)
		VALUES ($1,gen_random_uuid(),$2,$3,$4,$5)`, q.scope.UUID(), payment, action, []string{reason}, actor)
	return err
}
