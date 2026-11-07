package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

// scopeFixture populates every owned table, including intentionally unbound
// historical snapshot and token references and the quotation/invoice cycle.
func scopeFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	const q = `
INSERT INTO users(user_id,email,password_hash,password_reset_token) VALUES ('00000000-0000-0000-0000-000000000001','scope@example.test','hash','reset');
INSERT INTO customers(customer_id,name) VALUES ('00000000-0000-0000-0000-000000000001','Customer');
INSERT INTO vendors(vendor_id,name) VALUES ('00000000-0000-0000-0000-000000000001','Vendor');
INSERT INTO projects(project_id,customer_id,code,name,currency) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','P','Project','IDR');
INSERT INTO items(item_id,sku,name,item_type,revenue_account_id,expense_account_id) VALUES ('00000000-0000-0000-0000-000000000001','SKU','Item','SERVICE',1,1);
INSERT INTO cloudflare_targets(target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','z','r','example','A','1','2');
INSERT INTO quotations(quotation_id,customer_id,project_id,quotation_number,currency,amount) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','Q','IDR',100);
INSERT INTO invoices(invoice_id,customer_id,project_id,source_quote_id,invoice_type,currency,amount,balance_due,status,invoice_number) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','MILESTONE','IDR',100,80,'DRAFT','I');
UPDATE quotations SET converted_invoice_id='00000000-0000-0000-0000-000000000001';
INSERT INTO quotation_line_items(line_item_id,quotation_id,item_id,position,description,quantity,unit_price,amount) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001',1,'line',1,100,100);
INSERT INTO invoice_line_items(line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001',NULL,1,'line',1,100,100);
INSERT INTO bills(bill_id,vendor_id,project_id,currency,amount,balance_due) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','IDR',50,50);
INSERT INTO bill_line_items(line_item_id,bill_id,item_id,expense_account_id,position,description,quantity,unit_price,amount) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001',1,1,'line',1,50,50);
INSERT INTO payments(payment_id,invoice_id,customer_id,amount,currency,payer_name,payment_date,intake_source,external_ref,status) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001',20,'IDR','payer','2026-01-01','WEBHOOK','ref','PENDING');
INSERT INTO journal_entries(entry_id,source_type,source_id) VALUES ('00000000-0000-0000-0000-000000000001','UNKNOWN','00000000-0000-0000-0000-000000000099');
INSERT INTO journal_lines(line_id,entry_id,account_id,debit,currency,base_amount_debit) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001',1,20,'IDR',20);
INSERT INTO reconciliation_log(log_id,payment_id,actor_user_id,action) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','REVIEW');
INSERT INTO payment_account(business_profile_id,currency,instructions,is_default) VALUES (1,'IDR','bank',true);
INSERT INTO oauth_clients(client_id,redirect_uris) VALUES ('client','[]');
INSERT INTO oauth_refresh_tokens(jti,user_id,revoked) VALUES ('token','deleted or noncanonical user',false);
INSERT INTO recurring_draft_events(invoice_id,cycle_key,invoice_number,customer_name,amount,currency) VALUES ('00000000-0000-0000-0000-000000000099','cycle','old','old',123,'IDR');
INSERT INTO recurring_draft_deliveries(invoice_id,user_id,email) VALUES ('00000000-0000-0000-0000-000000000099','00000000-0000-0000-0000-000000000099','deleted@example.test');
INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001');
INSERT INTO hosting_operation_leases(item_id,operation,lease_token,lease_until) VALUES ('00000000-0000-0000-0000-000000000001','RESTORE','00000000-0000-0000-0000-000000000001',now());`
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
}

// cloneScope creates a complete second business while preserving the natural
// keys, using distinct globally unique entity IDs and fixing the document cycle.
func cloneScope(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	tables := []string{"business_profile", "chart_of_accounts", "users", "customers", "vendors", "fx_rates", "oauth_clients", "oauth_refresh_tokens", "projects", "items", "cloudflare_targets", "quotations", "invoices", "quotation_line_items", "invoice_line_items", "bills", "bill_line_items", "payments", "journal_entries", "journal_lines", "reconciliation_log", "payment_account", "recurring_draft_events", "recurring_draft_deliveries", "hosting_restore_intents", "hosting_operation_leases"}
	for _, name := range tables {
		// Identifiers come solely from the fixed list above.
		transform := `replace(replace(to_jsonb(x)::text, '00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002'), '00000000-0000-0000-0000-000000000099','00000000-0000-0000-0000-000000000098')::jsonb || jsonb_build_object('business_id',$1::text)`
		overrides := map[string]string{
			"business_profile":     ` || jsonb_build_object('id',2)`,
			"chart_of_accounts":    ` || jsonb_build_object('account_id',account_id+1000)`,
			"fx_rates":             ` || jsonb_build_object('rate_id',rate_id+1000)`,
			"payment_account":      ` || jsonb_build_object('id',id+1000,'business_profile_id',2)`,
			"items":                ` || jsonb_build_object('revenue_account_id',revenue_account_id+1000,'expense_account_id',expense_account_id+1000)`,
			"bill_line_items":      ` || jsonb_build_object('expense_account_id',expense_account_id+1000)`,
			"journal_lines":        ` || jsonb_build_object('account_id',account_id+1000)`,
			"oauth_clients":        ` || jsonb_build_object('client_id',client_id||'-b')`,
			"oauth_refresh_tokens": ` || jsonb_build_object('jti',jti||'-b')`,
			"quotations":           ` || jsonb_build_object('converted_invoice_id',NULL)`,
		}
		query := `INSERT INTO ` + name + ` SELECT (jsonb_populate_record(NULL::` + name + `, ` + transform + overrides[name] + `)).* FROM ` + name + ` x WHERE business_id <> $1::uuid`
		if _, err := db.Exec(query, id); err != nil {
			t.Fatalf("clone %s: %v", name, err)
		}
	}
	if _, err := db.Exec(`UPDATE quotations SET converted_invoice_id='00000000-0000-0000-0000-000000000002' WHERE business_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func checkForeignKeyIsolation(t *testing.T, db *sql.DB, a, b string) {
	t.Helper()
	rows, err := db.Query(`SELECT child.relname, parent.relname, ca.attname, pa.attname, ca.attnotnull
 FROM pg_constraint c JOIN pg_class child ON child.oid=c.conrelid JOIN pg_class parent ON parent.oid=c.confrelid
 JOIN pg_attribute ca ON ca.attrelid=c.conrelid AND ca.attnum=c.conkey[2]
 JOIN pg_attribute pa ON pa.attrelid=c.confrelid AND pa.attnum=c.confkey[2]
 JOIN pg_namespace n ON n.oid=child.relnamespace
 WHERE n.nspname='public' AND c.contype='f' AND parent.relname <> 'businesses' ORDER BY child.relname,c.conname`)
	if err != nil {
		t.Fatal(err)
	}
	type edge struct {
		child, parent, col, pcol string
		required                 bool
	}
	var edges []edge
	for rows.Next() {
		var e edge
		if err = rows.Scan(&e.child, &e.parent, &e.col, &e.pcol, &e.required); err != nil {
			t.Fatal(err)
		}
		edges = append(edges, e)
	}
	rows.Close()
	if len(edges) != 31 {
		t.Fatalf("unexpected FK inventory size %d", len(edges))
	}
	for _, e := range edges {
		t.Run(e.child+"/"+e.col, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			query := fmt.Sprintf(`UPDATE %q SET %q=(SELECT %q FROM %q WHERE business_id=$1 LIMIT 1) WHERE business_id=$2`, e.child, e.col, e.pcol, e.parent)
			res, err := tx.Exec(query, a, a)
			if err != nil {
				t.Fatalf("same-business reference: %v", err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				t.Fatal("missing child fixture")
			}
			if !e.required {
				if _, err = tx.Exec(fmt.Sprintf(`UPDATE %q SET %q=NULL WHERE business_id=$1`, e.child, e.col), a); err != nil {
					t.Fatalf("null reference: %v", err)
				}
			}
			if e.child == "hosting_operation_leases" {
				if _, err = tx.Exec(`DELETE FROM hosting_operation_leases WHERE business_id=$1`, b); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = tx.Exec(query, b, a); err == nil {
				t.Fatal("cross-business reference accepted")
			} else {
				var pgerr *pgconn.PgError
				if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
					t.Fatalf("expected FK violation, got %v", err)
				}
			}
		})
	}
}

func checkNaturalKeyConflicts(t *testing.T, db *sql.DB, a string) {
	t.Helper()
	cases := map[string]string{
		"account code":             `INSERT INTO chart_of_accounts(business_id,code,name,type) SELECT $1,code,name,type FROM chart_of_accounts WHERE business_id=$1 LIMIT 1`,
		"item sku":                 `INSERT INTO items(business_id,item_id,sku,name,item_type) VALUES ($1,gen_random_uuid(),'SKU','duplicate','SERVICE')`,
		"project code":             `INSERT INTO projects(business_id,project_id,customer_id,code,name,currency) VALUES ($1,gen_random_uuid(),'00000000-0000-0000-0000-000000000001','P','duplicate','IDR')`,
		"invoice number":           `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,invoice_number) VALUES ($1,gen_random_uuid(),'MILESTONE','IDR',1,1,'DRAFT','I')`,
		"quotation number":         `INSERT INTO quotations(business_id,quotation_id,customer_id,currency,amount,quotation_number) VALUES ($1,gen_random_uuid(),'00000000-0000-0000-0000-000000000001','IDR',1,'Q')`,
		"user email":               `INSERT INTO users(business_id,user_id,email,password_hash) VALUES ($1,gen_random_uuid(),'scope@example.test','hash')`,
		"reset token":              `INSERT INTO users(business_id,user_id,email,password_hash,password_reset_token) VALUES ($1,gen_random_uuid(),'other@example.test','hash','reset')`,
		"fx pair date":             `INSERT INTO fx_rates(business_id,from_currency,to_currency,rate,as_of_date,source) SELECT $1,from_currency,to_currency,rate,as_of_date,source FROM fx_rates WHERE business_id=$1 LIMIT 1`,
		"payment reference":        `INSERT INTO payments(business_id,payment_id,amount,currency,payer_name,payment_date,intake_source,external_ref,status) VALUES ($1,gen_random_uuid(),1,'IDR','payer','2026-01-01','WEBHOOK','ref','PENDING')`,
		"payment account currency": `INSERT INTO payment_account(business_id,business_profile_id,currency,instructions) VALUES ($1,1,'IDR','bank')`,
		"payment account default":  `INSERT INTO payment_account(business_id,business_profile_id,currency,instructions,is_default) VALUES ($1,1,'USD','bank',true)`,
		"profile":                  `INSERT INTO business_profile(business_id) VALUES ($1)`,
	}
	for name, q := range cases {
		t.Run("unique/"+name, func(t *testing.T) {
			_, err := db.Exec(q, a)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23505" {
				t.Fatalf("expected unique violation, got %v", err)
			}
		})
	}
}
