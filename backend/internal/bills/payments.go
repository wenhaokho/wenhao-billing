package bills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// BillPaymentRequest is the body of a bill payment, Python's
// RecordPaymentRequest (schemas/payment.py:50-56). Only the amount is used:
// no payment row is stored and nothing is posted (port note 8).
type BillPaymentRequest struct {
	Amount         platform.Dec4             `json:"amount" doc:"Above 0, at most 4 decimal places, at most the balance due."`
	PaymentDate    platform.Date             `json:"payment_date" doc:"Required, but not stored."`
	PayerName      platform.Optional[string] `json:"payer_name" doc:"Not stored."`
	PayerReference platform.Optional[string] `json:"payer_reference" doc:"Not stored."`
	Notes          platform.Optional[string] `json:"notes" doc:"Not stored."`
}

// VoidBillRequest is the body of a bill void, Python's untyped `payload:
// dict` (bills.py:119-136): a required JSON object whose other keys are
// ignored.
type VoidBillRequest struct {
	Reason json.RawMessage `json:"reason,omitempty" doc:"Any JSON value, written into the notes as Python prints it (a string as is, true as True, a list or object as Python's repr); left out or falsy (null, false, 0, \"\", [], {}) is \"no reason given\"."`
}

type paymentIn struct {
	BillID uuid.UUID `path:"bill_id"`
	Body   BillPaymentRequest
}

type voidIn struct {
	BillID uuid.UUID `path:"bill_id"`
	Body   VoidBillRequest
}

func (r routes) registerPayments(api huma.API, prefix string, tags []string) {
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-record-payment", Method: http.MethodPost, Path: prefix + "/{bill_id}/record-payment", Tags: tags,
		Summary:     "Record an outgoing payment against an OPEN or PARTIAL bill",
		Description: "The balance falls by the amount: to 0 is PAID, otherwise PARTIAL. More than the balance is refused. Nothing is posted to the ledger and no payment row is kept.",
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound},
	}), r.recordPayment)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-void", Method: http.MethodPost, Path: prefix + "/{bill_id}/void", Tags: tags,
		Summary:     "Void a DRAFT, OPEN or PARTIAL bill",
		Description: "Nothing is left due, and \"[voided] <reason>\" is appended to the notes on a new line.",
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound},
	}), r.void)
}

// notPositiveSentence is the 422 of Python's gt=0 constraint.
const notPositiveSentence = "Input should be greater than 0"

func (r routes) recordPayment(ctx context.Context, in *paymentIn) (*billBody, error) {
	amount := in.Body.Amount.Decimal()
	if amount.Sign() <= 0 {
		return nil, platform.FieldError("amount", notPositiveSentence)
	}
	return r.inTx(ctx, func(tx pgx.Tx) (BillOut, error) { return recordPayment(ctx, tx, in.BillID, amount) })
}

// recordPayment is Python's record_bill_payment (bills.py:83-116) in tx,
// with the bill read FOR UPDATE: two payments at once see each other's
// balance (Python lost an update). A DRAFT, VOID or PAID bill is refused,
// and so is more than the balance; the refusal prints the amount as
// Python's Decimal did, with the literal's own digits ("600.00").
func recordPayment(ctx context.Context, tx pgx.Tx, id uuid.UUID, amount decimal.Decimal) (BillOut, error) {
	q := billsdb.New(tx)
	cur, err := lock(ctx, q, id)
	if err != nil {
		return BillOut{}, err
	}
	switch cur.Status {
	case "DRAFT", "VOID", "PAID":
		return BillOut{}, refuse("bill_not_payable", "cannot record payment on %s bill", cur.Status)
	}
	if amount.GreaterThan(cur.BalanceDue) {
		return BillOut{}, refuse("payment_exceeds_balance", "amount %s exceeds balance due %s",
			pythonDecimal(amount), cur.BalanceDue.StringFixed(4))
	}
	balance, status := cur.BalanceDue.Sub(amount), "PARTIAL"
	if balance.Sign() <= 0 {
		balance, status = decimal.Zero, "PAID"
	}
	if err := q.SetBillBalance(ctx, billsdb.SetBillBalanceParams{BillID: id, BalanceDue: balance, Status: status}); err != nil {
		return BillOut{}, err
	}
	return get(ctx, q, id)
}

func (r routes) void(ctx context.Context, in *voidIn) (*billBody, error) {
	reason, truthy, err := pythonText(in.Body.Reason)
	if err != nil {
		return nil, err
	}
	if !truthy {
		reason = "no reason given"
	}
	return r.inTx(ctx, func(tx pgx.Tx) (BillOut, error) { return voidBill(ctx, tx, in.BillID, reason) })
}

// voidBill is Python's void_bill (billing_ap.py:118-125) in tx, with the
// bill read FOR UPDATE: a PAID or VOID bill is refused; any other becomes
// VOID with nothing due and the reason appended to its notes on a new line
// (so null notes start with the newline).
func voidBill(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) (BillOut, error) {
	q := billsdb.New(tx)
	cur, err := lock(ctx, q, id)
	if err != nil {
		return BillOut{}, err
	}
	if cur.Status == "PAID" || cur.Status == "VOID" {
		return BillOut{}, refuse("bill_not_voidable", "bill is %s and cannot be voided", cur.Status)
	}
	notes := ""
	if cur.Notes != nil {
		notes = *cur.Notes
	}
	notes += "\n[voided] " + reason
	if err := q.VoidBill(ctx, billsdb.VoidBillParams{BillID: id, Notes: &notes}); err != nil {
		return BillOut{}, err
	}
	return get(ctx, q, id)
}

// pythonDecimal is str(d) of Python's Decimal (to_sci_string) for the
// coefficient and exponent d was parsed with; shopspring keeps both as the
// literal wrote them, as Python's Decimal does: "600.00" stays 600.00, and
// 6e2 is 6E+2.
func pythonDecimal(d decimal.Decimal) string {
	sign, coef := "", d.Coefficient()
	if coef.Sign() < 0 {
		sign = "-"
		coef.Neg(coef)
	}
	digits, exp := coef.String(), int(d.Exponent())
	left := exp + len(digits)
	dot := 1
	if exp <= 0 && left > -6 {
		dot = left
	}
	var s string
	switch {
	case dot <= 0:
		s = "0." + strings.Repeat("0", -dot) + digits
	case dot >= len(digits):
		s = digits + strings.Repeat("0", dot-len(digits))
	default:
		s = digits[:dot] + "." + digits[dot:]
	}
	if left != dot {
		s += fmt.Sprintf("E%+d", left-dot)
	}
	return sign + s
}

// pythonText is the JSON value raw as Python's f-string prints what
// json.loads makes of it, and whether Python finds it truthy. A string is
// itself; null, booleans, numbers, lists and objects are Python's repr of
// None, True/False, int (a literal without '.', 'e' or 'E', any size),
// float (shortest repr; beyond the float range inf), list and dict (keys
// in first-seen order, a repeated key taking its last value). An empty raw
// (the key left out) is falsy.
func pythonText(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return pythonValue(dec, false)
}

// pythonValue reads one value from dec: its str when quoted is false (only
// a string differs), its repr when true.
func pythonValue(dec *json.Decoder, quoted bool) (string, bool, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", false, err
	}
	switch v := tok.(type) {
	case nil:
		return "None", false, nil
	case bool:
		if v {
			return "True", true, nil
		}
		return "False", false, nil
	case string:
		if quoted {
			return pythonRepr(v), v != "", nil
		}
		return v, v != "", nil
	case json.Number:
		return pythonNumber(string(v))
	case json.Delim:
		if v == '[' {
			return pythonList(dec)
		}
		return pythonDict(dec)
	}
	return "", false, fmt.Errorf("bills: unexpected JSON token %v", tok)
}

func pythonList(dec *json.Decoder) (string, bool, error) {
	var items []string
	for dec.More() {
		s, _, err := pythonValue(dec, true)
		if err != nil {
			return "", false, err
		}
		items = append(items, s)
	}
	if _, err := dec.Token(); err != nil { // ]
		return "", false, err
	}
	return "[" + strings.Join(items, ", ") + "]", len(items) > 0, nil
}

func pythonDict(dec *json.Decoder) (string, bool, error) {
	var keys []string
	values := map[string]string{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", false, err
		}
		key, ok := tok.(string)
		if !ok {
			return "", false, fmt.Errorf("bills: unexpected JSON object key %v", tok)
		}
		s, _, err := pythonValue(dec, true)
		if err != nil {
			return "", false, err
		}
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = s
	}
	if _, err := dec.Token(); err != nil { // }
		return "", false, err
	}
	items := make([]string, 0, len(keys))
	for _, k := range keys {
		items = append(items, pythonRepr(k)+": "+values[k])
	}
	return "{" + strings.Join(items, ", ") + "}", len(keys) > 0, nil
}

// pythonNumber is a JSON number as json.loads makes it: an int when the
// literal has no fraction or exponent, otherwise a float.
func pythonNumber(lit string) (string, bool, error) {
	if !strings.ContainsAny(lit, ".eE") {
		n, ok := new(big.Int).SetString(lit, 10)
		if !ok {
			return "", false, fmt.Errorf("bills: bad JSON integer %q", lit)
		}
		return n.String(), n.Sign() != 0, nil
	}
	// Beyond the float range (Python's inf) never gets here: Huma's own
	// parse of the body answers 400 for it first.
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return "", false, err
	}
	return pythonFloat(f), f != 0, nil
}

// pythonFloat is Python's repr of a finite f: the shortest round-trip
// digits, fixed notation (with ".0" when whole) for 1e-4 <= |f| < 1e16 and
// zero, otherwise exponent notation ("1e+16", "1.5e-05": Go's 'e' format
// also writes at least two exponent digits).
func pythonFloat(f float64) string { //nolint:forbidigo // not money: Python's repr of a JSON number
	if a := math.Abs(f); a != 0 && (a < 1e-4 || a >= 1e16) {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// pythonRepr is Python's repr of s: single quotes unless s holds a single
// quote and no double quote; backslash, the quote, \t, \n and \r escaped;
// other non-printable characters (Go's unicode.IsPrint standing in for
// str.isprintable) as \xNN, \uNNNN or \UNNNNNNNN.
func pythonRepr(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < ' ' || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f || unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}
