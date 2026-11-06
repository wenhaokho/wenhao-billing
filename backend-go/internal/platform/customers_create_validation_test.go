package platform

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCustomerCreateDifferential(t *testing.T) {
	cases := []string{`{"name":"Alice"}`, `{"name":"A","active":null,"matching_aliases":[]}`, `{"name":"A","matching_aliases":["a","b"],"active":"yes","default_currency":"usd"}`, `{"name":"A","contact_email":"Alice@EXAMPLE.COM","quotation_cc_emails":["a@example.com","A@example.com"," "]}`, `{"name":"A","default_wht_type":"PPH21"}`, `{"name":"A","default_wht_type":"PPH23","default_wht_rate":"5.5","default_wht_gross_up":true}`, `{}`, `{"name":null,"matching_aliases":null}`, `{"name":"","default_currency":"US","active":"bad"}`, `{"name":"A","matching_aliases":[1],"billing_cc_emails":["bad"]}`, `{"name":"A","default_wht_rate":"bad"}`, `{"name":"猫","contact_first_name":12}`, `{"name":"A","default_wht_type":""}`, `{"name":"A","default_wht_type":"BAD"}`, `{"name":"A","default_wht_type":"PPH21","default_wht_rate":100}`}
	cases = append(cases,
		`{"name":"A","quotation_cc_emails":null,"billing_cc_emails":[]}`,
		`{"name":"A","quotation_cc_emails":"a@example.com"}`,
		`{"name":"A","quotation_cc_emails":["bad",1]}`,
		`{"name":"A","quotation_cc_emails":[" a@example.com ","b@example.com","A@example.com"]}`,
		`{"name":"A","contact_email":"bad","matching_aliases":[null]}`,
		`{"name":"A","default_wht_type":null,"default_wht_rate":null,"default_wht_gross_up":null}`,
		`{"name":"A","default_wht_type":"PPH23","default_wht_rate":"0"}`,
		`{"name":"A","default_wht_type":"PPH23","default_wht_rate":"0.0001"}`,
		`{"name":"A","default_wht_type":"PPH23","default_wht_rate":"99.9999"}`,
		`{"name":"A","default_wht_type":"PPH23","default_wht_rate":"100"}`,
		`{"name":"A","default_wht_type":"NONE","default_wht_rate":"100","default_wht_gross_up":true}`,
		`{"name":"`+strings.Repeat("猫", 256)+`"}`,
		`{"name":"A","shipping_country":"`+strings.Repeat("x", 101)+`"}`,
	)
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			cmd := exec.Command("python", "-c", `import json,sys
from fastapi import FastAPI
from fastapi.testclient import TestClient
from app.schemas.customer import CustomerCreate
from app.services.customers import _normalize_wht
app=FastAPI()
@app.post('/')
def create(c:CustomerCreate):
 d=c.model_dump(mode='json',exclude_unset=True)
 try:
  from decimal import Decimal
  if d.get('default_wht_rate') is not None: d['default_wht_rate']=Decimal(d['default_wht_rate'])
  _normalize_wht(d,('NONE',None,False))
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
			got, errs, domain := parseCustomerCreate([]byte(body))
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

func TestCustomerCreateBoundsAndMalformed(t *testing.T) {
	for _, body := range []string{"", "{", `{} {}`, `[]`, `null`, `{"name":"é",}`} {
		_, errs, _ := parseCustomerCreate([]byte(body))
		if len(errs) == 0 {
			t.Fatalf("accepted %q", body)
		}
	}
	_, errs, _ := parseCustomerCreate([]byte(strings.Repeat(" ", (1<<20)+1)))
	if len(errs) != 1 || errs[0].Type != "body_too_large" {
		t.Fatalf("missing body bound: %+v", errs)
	}
	for _, body := range []string{`{"name":"A","default_wht_rate":1e1001}`, `{"name":"A","default_wht_rate":1e1000000000}`, `{"name":"A","default_wht_rate":"1e-1000000000"}`, `{"name":"A","active":1e1000000000}`} {
		_, errs, _ := parseCustomerCreate([]byte(body))
		if len(errs) == 0 {
			t.Fatalf("accepted unsafe exponent %s", body)
		}
	}
}

func TestCustomerWithholdingStoredSwitch(t *testing.T) {
	for _, tc := range []struct {
		typ, rate   string
		gross       bool
		input, want string
	}{
		{"PPH21", "7", true, `{"default_wht_type":"PPH23"}`, `{"default_wht_type":"PPH23","default_wht_rate":"2","default_wht_gross_up":true}`},
		{"PPH21", "7", true, `{"default_wht_type":null}`, `{"default_wht_type":"PPH21","default_wht_rate":"7","default_wht_gross_up":true}`},
		{"PPH21", "7", true, `{"default_wht_rate":null}`, `{"default_wht_type":"PPH21","default_wht_rate":"2.5","default_wht_gross_up":true}`},
	} {
		var m map[string]any
		json.Unmarshal([]byte(tc.input), &m)
		got, err := normalizeCustomerWithholding(m, tc.typ, tc.rate, tc.gross)
		if err != "" {
			t.Fatal(err)
		}
		cmd := exec.Command("python", "-c", `import json,sys
from decimal import Decimal
from app.services.customers import _normalize_wht
d=json.loads(sys.argv[1])
_normalize_wht(d,(sys.argv[2],Decimal(sys.argv[3]),sys.argv[4]=='true'))
if d.get('default_wht_rate') is not None: d['default_wht_rate']=str(d['default_wht_rate'])
print(json.dumps(d))`, tc.input, tc.typ, tc.rate, strconv.FormatBool(tc.gross))
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		var want any
		json.Unmarshal(out, &want)
		b, _ := json.Marshal(got)
		var actual any
		json.Unmarshal(b, &actual)
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("got %s want %s", b, tc.want)
		}
	}
}

func TestCustomerCreateTypedPresence(t *testing.T) {
	c, errs, domain := parseCustomerCreate([]byte(`{"name":"A","contact_phone":null,"active":false,"matching_aliases":[]}`))
	if len(errs) != 0 || domain != "" {
		t.Fatalf("errors: %v %s", errs, domain)
	}
	if !c.Name.Present || c.Name.Null || c.Name.Value != "A" || !c.ContactPhone.Present || !c.ContactPhone.Null || c.ContactPhone2.Present || !c.Active.Present || c.Active.Null || c.Active.Value || !c.MatchingAliases.Present || c.MatchingAliases.Null || len(c.MatchingAliases.Value) != 0 {
		t.Fatalf("presence lost: %+v", c)
	}
}

func TestCustomerCreateFastAPIJSONErrors(t *testing.T) {
	for _, body := range []string{"", `{`, `{} {}`, `null`, `[]`, `42`, `{"name":"é",}`, `{"name":]`} {
		t.Run(body, func(t *testing.T) {
			cmd := exec.Command("python", "-c", `import json,sys
from fastapi import FastAPI
from fastapi.testclient import TestClient
from app.schemas.customer import CustomerCreate
app=FastAPI()
@app.post('/')
def create(c:CustomerCreate): return {}
r=TestClient(app).post('/',content=sys.argv[1],headers={'content-type':'application/json'})
print(json.dumps(r.json()['detail']))`, body)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			_, errs, _ := parseCustomerCreate([]byte(body))
			b, _ := json.Marshal(errs)
			var got, want any
			json.Unmarshal(b, &got)
			json.Unmarshal(out, &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s want %s", b, out)
			}
		})
	}
}

func TestCustomerWithholdingExactStorage(t *testing.T) {
	for _, tc := range []struct {
		rate   string
		accept bool
	}{
		{`"0.00001"`, false}, {`"99.99999"`, false}, {`"0.00010"`, true}, {`"99.99990"`, true}, {`"0.0001"`, true}, {`"99.9999"`, true}, {`"0"`, false}, {`"100"`, false}, {`99.999999999999999999`, false}, {`"99.999999999999999999"`, false}, {`2.5`, true},
	} {
		body := `{"name":"A","default_wht_type":"PPH21","default_wht_rate":` + tc.rate + `}`
		_, errs, domain := parseCustomerCreate([]byte(body))
		if (len(errs) == 0 && domain == "") != tc.accept {
			t.Errorf("rate %s: errors=%v domain=%q", tc.rate, errs, domain)
		}
	}
	c, errs, domain := parseCustomerCreate([]byte(`{"name":"A","default_wht_type":"PPH21"}`))
	if len(errs) > 0 || domain != "" || c.DefaultWhtRate.Value != "2.5" {
		t.Fatalf("default: %+v %v %s", c, errs, domain)
	}
}

func TestCustomerCCLargeStableDeduplication(t *testing.T) {
	addresses := make([]string, 0, 12000)
	for i := 0; i < 6000; i++ {
		addresses = append(addresses, fmt.Sprintf("user%d@example.com", i))
	}
	for i := 0; i < 6000; i++ {
		addresses = append(addresses, fmt.Sprintf("USER%d@example.com", i))
	}
	b, _ := json.Marshal(map[string]any{"name": "A", "billing_cc_emails": addresses})
	c, errs, domain := parseCustomerCreate(b)
	if len(errs) > 0 || domain != "" {
		t.Fatalf("%v %s", errs, domain)
	}
	if len(c.BillingCcEmails.Value) != 6000 {
		t.Fatalf("got %d", len(c.BillingCcEmails.Value))
	}
	for i, v := range c.BillingCcEmails.Value {
		if v != addresses[i] {
			t.Fatalf("index %d: %s", i, v)
		}
	}
}

func TestCustomerRatePythonFloatSafetyException(t *testing.T) {
	for _, tc := range []struct{ body, pythonRate, errorType string }{
		{`{"name":"A","default_wht_type":"PPH21","default_wht_rate":99.999999999999999999}`, "100.0", "decimal_inexact"},
		{`{"name":"A","default_wht_type":"PPH21","default_wht_rate":"99.999999999999999999"}`, "99.999999999999999999", "decimal_precision"},
	} {
		cmd := exec.Command("python", "-c", `import json,sys
from app.schemas.customer import CustomerCreate
m=CustomerCreate.model_validate(json.loads(sys.argv[1])); print(str(m.default_wht_rate))`, tc.body)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(out)) != tc.pythonRate {
			t.Fatalf("Python got %s", out)
		}
		_, errs, domain := parseCustomerCreate([]byte(tc.body))
		if len(errs) != 1 || errs[0].Type != tc.errorType || domain != "" {
			t.Fatalf("unexpected error for %s: %v %s", tc.body, errs, domain)
		}
	}
}

func TestCustomerDiscardedWithholdingPrecisionPython(t *testing.T) {
	for _, tc := range []struct {
		body     string
		accepted bool
	}{
		{`{"name":"A","default_wht_type":"NONE","default_wht_rate":"0.00001"}`, true},
		{`{"name":"A","default_wht_rate":0.00001}`, true},
		{`{"name":"A","default_wht_type":"NONE","default_wht_rate":99.999999999999999999}`, true},
		{`{"name":"A","default_wht_type":null,"default_wht_rate":"99.99999"}`, true},
		{`{"name":"A","default_wht_type":"PPH21","default_wht_rate":"0.00001"}`, false},
		{`{"name":"A","default_wht_type":"PPH23","default_wht_rate":"99.99999"}`, false},
	} {
		cmd := exec.Command("python", "-c", `import json,sys
from app.schemas.customer import CustomerCreate
from app.services.customers import _normalize_wht
c=CustomerCreate.model_validate(json.loads(sys.argv[1]))
d=c.model_dump(exclude_unset=True);_normalize_wht(d,('NONE',None,False))
print(json.dumps(d,default=str))`, tc.body)
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		var python map[string]any
		if e = json.Unmarshal(out, &python); e != nil {
			t.Fatal(e)
		}
		c, errs, domain := parseCustomerCreate([]byte(tc.body))
		if tc.accepted {
			if len(errs) != 0 || domain != "" {
				t.Fatalf("%s: %v %s", tc.body, errs, domain)
			}
			b, _ := json.Marshal(c)
			var got any
			json.Unmarshal(b, &got)
			if !reflect.DeepEqual(got, python) {
				t.Fatalf("got %s Python %s", b, out)
			}
		} else if len(errs) == 0 && domain == "" {
			t.Fatalf("accepted active unsafe rate: %s; Python %s", tc.body, out)
		}
	}
}
