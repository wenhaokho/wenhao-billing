package platform

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestItemUpdateValidationDifferential(t *testing.T) {
	cases := []string{`{"revenue_account_id":"true","expense_account_id":"false","hosting_grace_days":"true"}`, `{"revenue_account_id":true,"expense_account_id":false,"hosting_grace_days":true}`, `{}`, `{"name":null,"is_sold":null}`, `{"name":"","sku":"x","is_hosting":true}`, `{"default_unit_price":"123.4500","hosting_grace_days":"3","active":"yes"}`, `{"name":12,"item_type":"BAD","default_currency":"US","active":"bad","hosting_grace_days":366}`, `{"cloudflare_target":{}}`, `{"cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"n","record_type":"A","live_content":"1","maintenance_content":"2","proxied":"bad"}}`, `{"default_unit_price":"0.00001"}`, `{"hosting_status":"BAD"}`, `{"cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"n","record_type":"A","live_content":"1","maintenance_content":"2","proxied":"yes"}}`, `{"cloudflare_target":{"zone_id":"z","record_id":"r","record_name":"n","record_type":"A","live_content":"1","maintenance_content":"2"}}`}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			cmd := exec.Command("python", "-c", `import json,sys
from pydantic import ValidationError
from app.schemas.item import ItemUpdate
try:
 m=ItemUpdate.model_validate(json.loads(sys.argv[1])); print(json.dumps({'fields':m.model_dump(mode='json',exclude_unset=True)}))
except ValidationError as e:
 errors=e.errors(include_url=False)
 for error in errors: error['loc']=['body']+list(error['loc'])
 print(json.dumps({'errors':errors}))`, body)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("python: %v", err)
			}
			var want map[string]any
			if err = json.Unmarshal(out, &want); err != nil {
				t.Fatal(err)
			}
			got, errs := parseItemUpdate([]byte(body))
			if len(errs) > 0 {
				b, _ := json.Marshal(errs)
				var x any
				json.Unmarshal(b, &x)
				wantErrors := want["errors"]
				if !reflect.DeepEqual(x, wantErrors) {
					t.Fatalf("errors got %s want %s", b, out)
				}
				return
			}
			if want["errors"] != nil {
				t.Fatalf("accepted: %s", out)
			}
			b, _ := json.Marshal(got)
			var x any
			json.Unmarshal(b, &x)
			if !reflect.DeepEqual(x, want["fields"]) {
				t.Fatalf("fields got %s want %s", b, out)
			}
		})
	}
}

func TestItemUpdateMalformedAndPresence(t *testing.T) {
	for _, body := range []string{`{`, `{} {}`, `null`, `[]`} {
		_, errors := parseItemUpdate([]byte(body))
		if len(errors) == 0 {
			t.Fatalf("accepted %s", body)
		}
	}
	u, errors := parseItemUpdate([]byte(`{"name":null,"is_sold":false,"cloudflare_target":null}`))
	if len(errors) != 0 {
		t.Fatal(errors)
	}
	if !u.Name.Present || !u.Name.Null || u.Sku.Present || !u.IsSold.Present || u.IsSold.Null || u.IsSold.Value || !u.CloudflareTarget.Null {
		t.Fatalf("presence lost: %+v", u)
	}
}

func TestItemUpdateFastAPIJSONErrors(t *testing.T) {
	for _, body := range []string{`{"unknown":["{",]}`, `{"unknown":{"a":"[\"x",}}`, `{"unknown":[1,]}`, `{"unknown":{"a":1,}}`, `{"unknown":[{"a":1,}]}`, `{"unknown":{"a":[1,]}}`, `{"unknown":[1,,2]}`, "", `{"name":"é",}`, "{", `{"name":`, `{} {}`, `[]`, `null`, `42`, `{"name":]`, `{"name":"x",}`} {
		t.Run(body, func(t *testing.T) {
			cmd := exec.Command("python", "-c", `import json,sys
from fastapi import FastAPI
from fastapi.testclient import TestClient
from app.schemas.item import ItemUpdate
app=FastAPI()
@app.patch("/")
def patch(item: ItemUpdate): return {}
r=TestClient(app).patch("/",content=sys.argv[1],headers={"content-type":"application/json"})
print(json.dumps(r.json()["detail"]))`, body)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("python: %v %s", err, out)
			}
			var want, got any
			if err = json.Unmarshal(out, &want); err != nil {
				t.Fatal(err)
			}
			_, errs := parseItemUpdate([]byte(body))
			b, _ := json.Marshal(errs)
			json.Unmarshal(b, &got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s want %s", b, out)
			}
		})
	}
}
func TestItemUpdateExtremeExponentBounded(t *testing.T) {
	if os.Getenv("ITEM_EXPONENT_CHILD") == "1" {
		for _, body := range []string{`{"revenue_account_id":1e1000000000}`, `{"hosting_grace_days":1e-1000000000}`, `{"active":1e1000000000}`, `{"active":1e-1000000000}`, `{"default_unit_price":1e1000000000}`, `{"default_unit_price":1e-1000000000}`, `{"expense_account_id":"1e1000000000"}`} {
			parseItemUpdate([]byte(body))
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestItemUpdateExtremeExponentBounded$")
	cmd.Env = append(os.Environ(), "ITEM_EXPONENT_CHILD=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("extreme exponent child: %v %s", err, out)
	}
}

func TestItemUpdateRatAllocationGuard(t *testing.T) {
	for _, s := range []string{"1e1001", "1e-1001", strings.Repeat("1", 4097)} {
		if _, ok := boundedItemRat(s); ok {
			t.Fatalf("accepted oversized rational %q", s)
		}
	}
	for _, s := range []string{"1e3", "1e-3", "123.0"} {
		if _, ok := boundedItemRat(s); !ok {
			t.Fatalf("rejected ordinary rational %q", s)
		}
	}
}
