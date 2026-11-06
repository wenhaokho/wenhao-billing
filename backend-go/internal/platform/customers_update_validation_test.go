package platform

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestCustomerUpdatePresenceAndWithholding(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{}`, `{}`},
		{`{"name":null,"matching_aliases":null}`, `{"name":null,"matching_aliases":null}`},
		{`{"default_wht_type":"PPH23"}`, `{"default_wht_type":"PPH23","default_wht_rate":"2","default_wht_gross_up":true}`},
		{`{"default_wht_type":null,"default_wht_gross_up":null}`, `{"default_wht_type":"PPH21","default_wht_rate":"3","default_wht_gross_up":true}`},
		{`{"default_wht_rate":null}`, `{"default_wht_type":"PPH21","default_wht_rate":"2.5","default_wht_gross_up":true}`},
		{`{"default_wht_type":"NONE"}`, `{"default_wht_type":"NONE","default_wht_rate":null,"default_wht_gross_up":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			c, e, d := parseCustomerUpdate([]byte(tc.body), customerWithholding{"PPH21", "3", true})
			if len(e) > 0 || d != "" {
				t.Fatalf("errors %v %s", e, d)
			}
			b, _ := json.Marshal(c)
			var got, want any
			json.Unmarshal(b, &got)
			json.Unmarshal([]byte(tc.want), &want)
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(want)
			if string(gb) != string(wb) {
				t.Fatalf("got %s want %s", gb, wb)
			}
		})
	}
}

func TestCustomerUpdateDifferential(t *testing.T) {
	fields := []string{"name", "matching_aliases", "active", "contact_first_name", "contact_last_name", "contact_email", "quotation_cc_emails", "billing_cc_emails", "contact_phone", "contact_phone_2", "account_number", "website", "notes", "default_currency", "billing_address", "billing_address1", "billing_address2", "billing_country", "billing_state", "billing_city", "billing_postal_code", "ship_to_name", "shipping_address1", "shipping_address2", "shipping_country", "shipping_state", "shipping_city", "shipping_postal_code", "shipping_phone", "shipping_delivery_instructions", "default_wht_type", "default_wht_rate", "default_wht_gross_up"}
	cases := []string{`{}`, `{"unknown":1}`, `{"name":"","active":"bad","contact_email":"bad","default_currency":"US"}`, `{"matching_aliases":[1,null],"billing_cc_emails":["bad",1]}`, `{"quotation_cc_emails":[" A@example.com ","a@EXAMPLE.COM"," "]}`, `{"contact_email":"Alice@EXAMPLE.COM","name":"猫"}`, `{"default_wht_type":"BAD"}`, `{"default_wht_rate":"-1"}`, `{"default_wht_rate":"100"}`, `{"default_wht_type":"NONE","default_wht_rate":"100"}`, `{"default_wht_type":"PPH23"}`}
	for _, field := range fields {
		cases = append(cases, `{"`+field+`":null}`, `{"`+field+`":{}}`)
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			cmd := exec.Command("python", "-c", `import json,sys
from fastapi import FastAPI
from fastapi.testclient import TestClient
from app.schemas.customer import CustomerUpdate
from app.services.customers import _normalize_wht
from decimal import Decimal
app=FastAPI()
@app.post('/')
def update(c:CustomerUpdate):
 d=c.model_dump(exclude_unset=True)
 try:
  _normalize_wht(d,('PPH21',Decimal('3'),True))
  if d.get('default_wht_rate') is not None: d['default_wht_rate']=str(d['default_wht_rate'])
  return {'fields':d}
 except ValueError as e: return {'domain_error':str(e)}
r=TestClient(app).post('/',content=sys.argv[1],headers={'content-type':'application/json'})
print(json.dumps(r.json(),default=str))`, body)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var want any
			if err = json.Unmarshal(out, &want); err != nil {
				t.Fatal(err)
			}
			got, errs, domain := parseCustomerUpdate([]byte(body), customerWithholding{"PPH21", "3", true})
			var result any
			if len(errs) > 0 {
				result = map[string]any{"detail": errs}
			} else if domain != "" {
				result = map[string]any{"domain_error": domain}
			} else {
				result = map[string]any{"fields": got}
			}
			b, _ := json.Marshal(result)
			var actual any
			json.Unmarshal(b, &actual)
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("got %s want %s", b, out)
			}
		})
	}
}

func TestCustomerUpdateBounds(t *testing.T) {
	for _, body := range []string{"", "{", `{} {}`, `[]`, `null`, `{"name":"é",}`, strings.Repeat(" ", 1<<20+1), `{"default_wht_rate":"1e-1000000000"}`, `{"default_wht_rate":1e1000000000}`, `{"active":1e1000000000}`, `{"default_wht_rate":"0.00001"}`, `{"default_wht_rate":0.1234567890123456789}`} {
		_, errs, _ := parseCustomerUpdate([]byte(body), customerWithholding{"PPH21", "3", true})
		if len(errs) == 0 {
			t.Errorf("accepted %q", body[:min(len(body), 100)])
		}
	}
	for _, body := range []string{`{"default_wht_rate":"0.0001"}`, `{"default_wht_rate":"99.9999"}`, `{"default_wht_type":"NONE","default_wht_rate":"0.00001"}`} {
		_, errs, domain := parseCustomerUpdate([]byte(body), customerWithholding{"PPH21", "3", true})
		if len(errs) > 0 || domain != "" {
			t.Errorf("rejected %s: %v %s", body, errs, domain)
		}
	}
}

func TestCustomerUpdateGuardErrors(t *testing.T) {
	for _, tc := range []struct{ body, kind string }{
		{strings.Repeat(" ", 1<<20+1), "body_too_large"},
		{`{"default_wht_rate":0.1234567890123456789}`, "decimal_inexact"},
		{`{"default_wht_rate":"0.00001"}`, "decimal_precision"},
	} {
		_, errs, _ := parseCustomerUpdate([]byte(tc.body), customerWithholding{"PPH21", "3", true})
		if len(errs) != 1 || errs[0].Type != tc.kind {
			t.Errorf("want %s, got %v", tc.kind, errs)
		}
	}
}

func TestCustomerUpdateSharedGuards(t *testing.T) {
	for _, rate := range []string{strings.Repeat("0", 4097), "0e00000", "0e1001"} {
		_, errs, _ := parseCustomerUpdate([]byte(`{"default_wht_type":"NONE","default_wht_rate":"`+rate+`"}`), customerWithholding{"NONE", "", false})
		if len(errs) == 0 || errs[0].Type != "decimal_parsing" {
			t.Errorf("expected bounded decimal rejection for %q", rate[:min(20, len(rate))])
		}
	}
	for _, body := range []string{`{"contact_email":"bad"}`, `{"billing_cc_emails":["bad"]}`} {
		_, errs, _ := parseCustomerUpdate([]byte(body), customerWithholding{"NONE", "", false})
		if len(errs) == 0 {
			t.Errorf("accepted %s", body)
		}
	}
	c, errs, _ := parseCustomerUpdate([]byte(`{"billing_cc_emails":[" A@example.com ","a@EXAMPLE.COM"," "]}`), customerWithholding{"NONE", "", false})
	if len(errs) > 0 || !reflect.DeepEqual(c.BillingCcEmails.Value, []string{"A@example.com"}) {
		t.Fatalf("bad CC normalization: %+v %v", c.BillingCcEmails, errs)
	}
}

func TestCustomerUpdateUnicodeCCAndDecimalUnderscores(t *testing.T) {
	cases := []string{`{"billing_cc_emails":["İ@example.com","i@example.com","I@example.com"]}`, `{"billing_cc_emails":["ΟΣ@example.com","ος@example.com","οσ@example.com"]}`, `{"default_wht_rate":"1_0"}`, `{"default_wht_rate":"1_0.0_0"}`, `{"default_wht_rate":"_1_"}`, `{"default_wht_rate":"1__0"}`, `{"default_wht_rate":"1_e_0"}`, `{"default_wht_rate":"1_.2"}`, `{"default_wht_rate":"_._"}`, `{"default_wht_rate":"1_e_"}`, `{"default_wht_rate":"1__x"}`, `{"default_wht_rate":"1_/2"}`}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			cmd := exec.Command("python", "-c", `import json,sys
from decimal import Decimal
from pydantic import ValidationError
from app.schemas.customer import CustomerUpdate
from app.services.customers import _normalize_wht
try:
 d=CustomerUpdate.model_validate_json(sys.argv[1]).model_dump(exclude_unset=True)
 _normalize_wht(d,('PPH21',Decimal('3'),True))
 if d.get('default_wht_rate') is not None: d['default_wht_rate']=str(d['default_wht_rate'])
 print(json.dumps({'fields':d},ensure_ascii=False))
except ValidationError as e: print(json.dumps({'invalid':True}))
except ValueError as e: print(json.dumps({'domain_error':str(e)}))`, body)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var want any
			json.Unmarshal(out, &want)
			got, errs, domain := parseCustomerUpdate([]byte(body), customerWithholding{"PPH21", "3", true})
			var result any
			if len(errs) > 0 {
				result = map[string]any{"invalid": true}
			} else if domain != "" {
				result = map[string]any{"domain_error": domain}
			} else {
				result = map[string]any{"fields": got}
			}
			b, _ := json.Marshal(result)
			var actual any
			json.Unmarshal(b, &actual)
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("got %s want %s", b, out)
			}
		})
	}
}

func TestCustomerUpdateUnderscoreGuards(t *testing.T) {
	for _, tc := range []struct{ rate, kind string }{{strings.Repeat("_", 4097) + "1", "decimal_parsing"}, {"1e0_0_0_0_0", "decimal_parsing"}, {"0.000_01", "decimal_precision"}, {"1_e_1001", "decimal_parsing"}} {
		_, errs, _ := parseCustomerUpdate([]byte(`{"default_wht_rate":"`+tc.rate+`"}`), customerWithholding{"PPH21", "3", true})
		if len(errs) != 1 || errs[0].Type != tc.kind {
			t.Errorf("expected %s, got %v", tc.kind, errs)
		}
	}
}
