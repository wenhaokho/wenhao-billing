package platform

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

func TestEmailSharedPythonDifferential(t *testing.T) {
	cases := []string{`"John <Doe>" <a@example.com>`, `"" <a@example.com>`, `"Name\" <a@example.com>`, `"John \"Doe\"" <a@example.com>`, `"John "Doe"" <a@example.com>`, `"Name (Comment)" <a@example.com>`, `"Name <a@example.com>`, `"John \"Doe\"" <a@example.com> (Comment)`, "foo@example.com (Comment)", "foo(comment)@example.com", "Name (Comment) <foo@example.com>", "foo@example.com\n", "foo@example.com\r", "Name <foo@example.com>\n", "foo@example.com\t", "\tfoo@example.com\t", " foo@example.com ", "Name <Test@EXAMPLE.COM>", "<foo@example.com>", "foo@example.com\v"}
	b, _ := json.Marshal(cases)
	cmd := exec.Command("python", "-c", `import json,os
from pydantic import EmailStr,TypeAdapter
out=[]
for s in json.loads(os.environ['EMAIL_CASES']):
 try: out.append([True,TypeAdapter(EmailStr).validate_python(s)])
 except Exception: out.append([False,''])
print(json.dumps(out))`)
	cmd.Env = append(os.Environ(), "EMAIL_CASES="+string(b))
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	var expected [][]any
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for i, s := range cases {
		value, reason := normalizeEmail(s)
		got := []any{reason == "", value}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("%q: %v vs Python %v", s, got, expected[i])
		}
	}
}

func TestEmailSharedCallersRejectCommentsAndNewlines(t *testing.T) {
	valid := `"John \"Doe\"" <a@example.com>`
	payload, _ := json.Marshal(map[string]any{"name": "Test", "email": valid, "contact_email": valid, "password": "abcdefgh"})
	if p, errs := parseVendorCreate(payload); len(errs) != 0 || p.ContactEmail.String != "a@example.com" {
		t.Errorf("vendor display name: %+v %v", p, errs)
	}
	if _, errs, _ := parseCustomerCreate(payload); len(errs) != 0 {
		t.Errorf("customer display name: %v", errs)
	}
	if got, ok := loginValidation(httptest.NewRecorder(), jsonTestRequest(payload)); !ok || got.Email != "a@example.com" {
		t.Errorf("login display name: %+v %v", got, ok)
	}
	for _, email := range []string{"foo@example.com (Comment)", "foo@example.com\n"} {
		payload, _ := json.Marshal(map[string]any{"name": "Test", "email": email, "contact_email": email, "password": "abcdefgh"})
		if _, errs := parseVendorCreate(payload); len(errs) == 0 {
			t.Errorf("vendor accepted %q", email)
		}
		if _, errs, _ := parseCustomerCreate(payload); len(errs) == 0 {
			t.Errorf("customer accepted %q", email)
		}
		if _, ok := loginValidation(httptest.NewRecorder(), jsonTestRequest(payload)); ok {
			t.Errorf("login accepted %q", email)
		}
	}
}

func jsonTestRequest(body []byte) *http.Request {
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func withJSONContentType(r *http.Request) *http.Request {
	r.Header.Set("Content-Type", "application/json")
	return r
}
