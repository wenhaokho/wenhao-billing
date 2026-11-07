package migrations

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedaccount"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedbills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedcustomer"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedfx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeditem"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedpayment"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedprofile"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedproject"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedquotations"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedvendor"
)

func testScopedProfileAdapter(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var a, b string
	if err = tx.QueryRow(ctx, `SELECT business_id::text FROM businesses LIMIT 1`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO businesses(business_id) VALUES(gen_random_uuid()) RETURNING business_id::text`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	scope, err := business.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = scopedprofile.ReplaceAccounts(ctx, tx, business.ID{}, nil); err == nil {
		t.Fatal("zero business accepted")
	}
	if err = scopedprofile.ReplaceAccounts(ctx, tx, scope, []scopedprofile.Account{{Currency: "IDR", Instructions: "new B", IsDefault: true}}); err != nil {
		t.Fatal(err)
	}
	profile, err := scopedprofile.Update(ctx, tx, scope, []byte(`{"name":"B profile","business_id":"`+a+`"}`))
	if err != nil || profile.BusinessID != scope.UUID() || profile.Name.String != "B profile" {
		t.Fatalf("profile update: %v %v", profile, err)
	}
	loaded, err := scopedprofile.Get(ctx, tx, scope)
	if err != nil || loaded.ID != profile.ID || loaded.Name.String != "B profile" {
		t.Fatalf("profile read: %v %v", loaded, err)
	}
	profileAccounts, err := scopedprofile.ListAccounts(ctx, tx, scope)
	if err != nil || len(profileAccounts) != 1 || profileAccounts[0].BusinessID != scope.UUID() {
		t.Fatalf("profile account list: %v %v", profileAccounts, err)
	}
	var profileID, aCount, bCount int
	if err = tx.QueryRow(ctx, `SELECT id FROM business_profile WHERE business_id=$1`, b).Scan(&profileID); err != nil || profileID == 1 {
		t.Fatalf("B profile id %d: %v", profileID, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE business_id=$1),count(*) FILTER(WHERE business_id=$2) FROM payment_account`, a, b).Scan(&aCount, &bCount); err != nil || aCount != 1 || bCount != 1 {
		t.Fatalf("account counts A=%d B=%d: %v", aCount, bCount, err)
	}
	if err = scopedprofile.ReplaceAccounts(ctx, tx, scope, nil); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE business_id=$1),count(*) FILTER(WHERE business_id=$2) FROM payment_account`, a, b).Scan(&aCount, &bCount); err != nil || aCount != 1 || bCount != 0 {
		t.Fatalf("replacement counts A=%d B=%d: %v", aCount, bCount, err)
	}
	owner, err := business.Parse(a)
	if err != nil {
		t.Fatal(err)
	}
	var code, typ string
	if err = tx.QueryRow(ctx, `SELECT code,type FROM chart_of_accounts WHERE business_id=$1 LIMIT 1`, a).Scan(&code, &typ); err != nil {
		t.Fatal(err)
	}
	fields := scopedaccount.Fields{Code: code, Name: "B account", Type: typ, Active: true}
	created, err := scopedaccount.Create(ctx, tx, scope, fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scopedaccount.Get(ctx, tx, owner, created.AccountID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign get: %v", err)
	}
	if _, err = scopedaccount.Lock(ctx, tx, owner, created.AccountID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign lock: %v", err)
	}
	if _, err = scopedaccount.Update(ctx, tx, owner, created.AccountID, fields); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign update: %v", err)
	}
	rows, err := scopedaccount.List(ctx, tx, scope, pgtype.Bool{})
	if err != nil || len(rows) != 1 || rows[0].AccountID != created.AccountID {
		t.Fatalf("B list: %v %v", rows, err)
	}
	vendor, err := scopedvendor.Create(ctx, tx, scope, scopedvendor.Fields{Name: "B vendor", DefaultCurrency: "IDR", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scopedvendor.Get(ctx, tx, owner, vendor.VendorID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign vendor get: %v", err)
	}
	if _, err = scopedvendor.Lock(ctx, tx, owner, vendor.VendorID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign vendor lock: %v", err)
	}
	if _, err = scopedvendor.Update(ctx, tx, owner, vendor.VendorID, []byte(`{"name":"foreign"}`)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign vendor update: %v", err)
	}
	if _, err = scopedvendor.Deactivate(ctx, tx, owner, vendor.VendorID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign vendor deactivate: %v", err)
	}
	vendors, err := scopedvendor.List(ctx, tx, scope, pgtype.Bool{})
	if err != nil || len(vendors) != 1 || vendors[0].VendorID != vendor.VendorID {
		t.Fatalf("B vendor list: %v %v", vendors, err)
	}
	var foreignID pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT payment_id FROM payments WHERE business_id <> $1 LIMIT 1`, a).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedpayment.Get(ctx, tx, owner, foreignID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign payment get: %v", err)
	}
	logs, err := scopedpayment.Logs(ctx, tx, owner, foreignID)
	if err != nil || len(logs) != 0 {
		t.Fatalf("foreign payment logs: %v %v", logs, err)
	}
	audit, err := scopedpayment.Audit(ctx, tx, owner, scopedpayment.AuditFilter{RowLimit: 100})
	if err != nil || len(audit) != 1 || audit[0].PaymentID == foreignID {
		t.Fatalf("payment audit isolation: %v %v", audit, err)
	}
	var from, to string
	var date pgtype.Date
	if err = tx.QueryRow(ctx, `SELECT from_currency,to_currency,as_of_date FROM fx_rates WHERE business_id=$1 LIMIT 1`, a).Scan(&from, &to, &date); err != nil {
		t.Fatal(err)
	}
	var rate pgtype.Numeric
	if err = rate.Scan("2.0000"); err != nil {
		t.Fatal(err)
	}
	bRate, err := scopedfx.Create(ctx, tx, scope, scopedfx.Rate{From: from, To: to, Date: date, Value: rate, Source: "MANUAL"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scopedfx.Delete(ctx, tx, owner, bRate.RateID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign FX deletion: %v", err)
	}
	if err = scopedfx.UpdateSync(ctx, tx, owner, bRate.RateID, rate); err != nil {
		t.Fatal(err)
	}
	remaining, err := scopedfx.FindSync(ctx, tx, scope, from, to, date)
	if err != nil || remaining.Source != "MANUAL" {
		t.Fatalf("foreign FX update changed B: %v %v", remaining, err)
	}
	if err = scopedfx.LockSync(ctx, tx, owner); err != nil {
		t.Fatal(err)
	}
	other, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(ctx)
	otherTx, err := other.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer otherTx.Rollback(ctx)
	var acquired bool
	if err = otherTx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('fx-sync:' || $1::uuid::text, 0))`, a).Scan(&acquired); err != nil || acquired {
		t.Fatalf("same-business FX lock acquired=%v: %v", acquired, err)
	}
	if err = otherTx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('fx-sync:' || $1::uuid::text, 0))`, b).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("other-business FX lock acquired=%v: %v", acquired, err)
	}
	var foreignProject, foreignBusiness, customer pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT project_id,business_id FROM projects WHERE business_id<>$1 LIMIT 1`, a).Scan(&foreignProject, &foreignBusiness); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedproject.GetProject(ctx, tx, owner, dbqueryv8.GetProjectParams{BusinessID: foreignBusiness, ProjectID: foreignProject}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign project get: %v", err)
	}
	if _, err = scopedproject.LockProject(ctx, tx, owner, dbqueryv8.LockProjectParams{ProjectID: foreignProject}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign project lock: %v", err)
	}
	if err = tx.QueryRow(ctx, `SELECT customer_id FROM customers WHERE business_id=$1 LIMIT 1`, a).Scan(&customer); err != nil {
		t.Fatal(err)
	}
	_, err = scopedproject.CreateProject(ctx, tx, scope, dbqueryv8.CreateProjectParams{CustomerID: customer, Code: "foreign", Name: "foreign", Currency: "IDR", Status: "ACTIVE"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign customer project insert: %v", err)
	}
	refs, err := scopedproject.CountProjectReferences(ctx, tx, owner, dbqueryv8.CountProjectReferencesParams{ProjectID: foreignProject})
	if err != nil || refs != 0 {
		t.Fatalf("foreign project references=%d: %v", refs, err)
	}
	var foreignItem, targetID pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT item_id FROM items WHERE business_id=$1 LIMIT 1`, foreignBusiness).Scan(&foreignItem); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	target := dbqueryv8.UpsertItemTargetParams{TargetID: targetID, ItemID: foreignItem, ZoneID: "new-zone", RecordID: "r", RecordName: "example", RecordType: "A", LiveContent: "1", MaintenanceContent: "2"}
	if _, err = scopeditem.UpsertItemTarget(ctx, tx, owner, target); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign target upsert: %v", err)
	}
	foreignScope, err := business.Parse(foreignBusiness.String())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := scopeditem.UpsertItemTarget(ctx, tx, foreignScope, target)
	if err != nil || updated.ZoneID != "new-zone" {
		t.Fatalf("own target upsert: %v %v", updated, err)
	}
	var foreignCustomer pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT customer_id FROM customers WHERE business_id=$1 LIMIT 1`, foreignBusiness).Scan(&foreignCustomer); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedcustomer.GetCustomer(ctx, tx, owner, dbqueryv8.GetCustomerParams{CustomerID: foreignCustomer, BusinessID: foreignBusiness}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign customer get: %v", err)
	}
	if _, err = scopedcustomer.LockCustomer(ctx, tx, owner, dbqueryv8.LockCustomerParams{CustomerID: foreignCustomer}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign customer lock: %v", err)
	}
	if _, err = scopedcustomer.DeactivateCustomer(ctx, tx, owner, dbqueryv8.DeactivateCustomerParams{CustomerID: foreignCustomer}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign customer deactivate: %v", err)
	}
	invoices, err := scopedcustomer.CustomerInvoices(ctx, tx, owner, dbqueryv8.CustomerInvoicesParams{CustomerID: foreignCustomer})
	if err != nil || len(invoices) != 0 {
		t.Fatalf("foreign customer invoices: %v %v", invoices, err)
	}
	var userID pgtype.UUID
	var email string
	if err = tx.QueryRow(ctx, `SELECT user_id,email FROM users WHERE business_id=$1`, foreignBusiness).Scan(&userID, &email); err != nil {
		t.Fatal(err)
	}
	user, err := scopeduser.GetSessionUserByEmail(ctx, tx, owner, dbqueryv8.GetSessionUserByEmailParams{Email: email})
	if err != nil || user.UserID == userID {
		t.Fatalf("same email resolved foreign user: %v %v", user, err)
	}
	if _, err = scopeduser.GetSessionUserByID(ctx, tx, owner, dbqueryv8.GetSessionUserByIDParams{UserID: userID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign session user: %v", err)
	}
	if _, err = scopeduser.SetUserPassword(ctx, tx, owner, dbqueryv8.SetUserPasswordParams{UserID: userID, PasswordHash: "foreign"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign password update: %v", err)
	}
	var billID, quoteID, entryID pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT bill_id FROM bills WHERE business_id=$1 LIMIT 1`, foreignBusiness).Scan(&billID); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedbills.GetBill(ctx, tx, owner, dbqueryv8.GetBillParams{BillID: billID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign bill: %v", err)
	}
	if err = tx.QueryRow(ctx, `SELECT quotation_id FROM quotations WHERE business_id=$1 LIMIT 1`, foreignBusiness).Scan(&quoteID); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedquotations.LockQuotation(ctx, tx, owner, dbqueryv8.LockQuotationParams{QuotationID: quoteID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign quotation: %v", err)
	}
	if err = tx.QueryRow(ctx, `SELECT entry_id FROM journal_entries WHERE business_id=$1 LIMIT 1`, foreignBusiness).Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedledger.LedgerLockEntry(ctx, tx, owner, dbqueryv8.LedgerLockEntryParams{EntryID: entryID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign journal: %v", err)
	}
	var invoiceID pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT invoice_id FROM invoices WHERE business_id=$1 LIMIT 1`, foreignBusiness).Scan(&invoiceID); err != nil {
		t.Fatal(err)
	}
	if _, err = scopedinvoices.GetInvoice(ctx, tx, owner, dbqueryv8.GetInvoiceParams{InvoiceID: invoiceID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign invoice detail: %v", err)
	}
	if _, err = scopedinvoices.LockInvoice(ctx, tx, owner, dbqueryv8.LockInvoiceParams{InvoiceID: invoiceID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign invoice lock: %v", err)
	}
	lines, err := scopedinvoices.InvoiceLinesByIDs(ctx, tx, owner, dbqueryv8.InvoiceLinesByIDsParams{InvoiceIds: []pgtype.UUID{invoiceID}})
	if err != nil || len(lines) != 0 {
		t.Fatalf("foreign invoice lines: %v %v", lines, err)
	}
}
