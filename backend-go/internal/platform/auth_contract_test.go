package platform

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Expected payloads were captured from FastAPI TestClient using the Python LoginRequest.
func TestLoginValidationContract(t *testing.T) {
	cases := []struct{ body, want string }{
		{"", `{"detail":[{"type":"missing","loc":["body"],"msg":"Field required","input":null}]}`},
		{"{", `{"detail":[{"type":"json_invalid","loc":["body",1],"msg":"JSON decode error","input":{},"ctx":{"error":"Expecting property name enclosed in double quotes"}}]}`},
		{`{"email":`, `{"detail":[{"type":"json_invalid","loc":["body",9],"msg":"JSON decode error","input":{},"ctx":{"error":"Expecting value"}}]}`},
		{`{}`, `{"detail":[{"type":"missing","loc":["body","email"],"msg":"Field required","input":{}},{"type":"missing","loc":["body","password"],"msg":"Field required","input":{}}]}`},
		{`null`, `{"detail":[{"type":"model_attributes_type","loc":["body"],"msg":"Input should be a valid dictionary or object to extract fields from","input":null}]}`},
		{`[]`, `{"detail":[{"type":"model_attributes_type","loc":["body"],"msg":"Input should be a valid dictionary or object to extract fields from","input":[]}]}`},
		{`{"email":"bad","password":"x"}`, `{"detail":[{"type":"value_error","loc":["body","email"],"msg":"value is not a valid email address: An email address must have an @-sign.","input":"bad","ctx":{"reason":"An email address must have an @-sign."}}]}`},
		{`{"email":"a@example.com"}`, `{"detail":[{"type":"missing","loc":["body","password"],"msg":"Field required","input":{"email":"a@example.com"}}]}`},
		{`{"email":null,"password":4,"remember":"bad"}`, `{"detail":[{"type":"string_type","loc":["body","email"],"msg":"Input should be a valid string","input":null},{"type":"string_type","loc":["body","password"],"msg":"Input should be a valid string","input":"[REDACTED]"},{"type":"bool_parsing","loc":["body","remember"],"msg":"Input should be a valid boolean, unable to interpret input","input":"bad"}]}`},
		{`{"email":"a@example.com","password":"x","remember":null}`, `{"detail":[{"type":"bool_type","loc":["body","remember"],"msg":"Input should be a valid boolean","input":null}]}`},
		{`{"email":"a@localhost","password":"x"}`, `{"detail":[{"type":"value_error","loc":["body","email"],"msg":"value is not a valid email address: The part after the @-sign is not valid. It should have a period.","input":"a@localhost","ctx":{"reason":"The part after the @-sign is not valid. It should have a period."}}]}`},
	}
	s := sessionHandler{}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.login(w, withJSONContentType(httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(tc.body))))
			var got, want any
			if json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(tc.want), &want) != nil || w.Code != 422 || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %d %s want %s", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}

func TestValidationRedactsPasswords(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"password":"secret-sentinel"}`, `{"email":"user@example.com","password":{"nested":"secret-sentinel"}}`, `[{"password":"secret-sentinel"}]`, `{"password":[{"password":"secret-sentinel"}]}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, withJSONContentType(httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))))
		var result map[string]any
		if w.Code != 422 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result["detail"] == nil || strings.Contains(w.Body.String(), "secret-sentinel") {
			t.Fatalf("unsafe response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestEmailDomainLabels(t *testing.T) {
	for _, email := range []string{"a@-example-.com", "a@example-.com", "a@foo.-bar.com", "a@foo.bar-.com", "a@example!.com", "a@" + strings.Repeat("x", 64) + ".com"} {
		if _, reason := normalizeEmail(email); reason == "" {
			t.Errorf("accepted %q", email)
		}
	}
}

// Cases and messages captured from the Python LoginRequest EmailStr validator.
func TestEmailPythonDifferential(t *testing.T) {
	cases := []struct{ email, want, reason string }{
		{".foo@example.com", "", "An email address cannot start with a period."},
		{"foo.@example.com", "", "An email address cannot have a period immediately before the @-sign."},
		{"foo..bar@example.com", "", "An email address cannot have two periods in a row."},
		{"foo@example.test", "", "The part after the @-sign is a special-use or reserved name that cannot be used with email."},
		{"foo@sub.example.invalid", "", "The part after the @-sign is a special-use or reserved name that cannot be used with email."},
		{"foo@example.local", "", "The part after the @-sign is a special-use or reserved name that cannot be used with email."},
		{"foo@example.onion", "", "The part after the @-sign is a special-use or reserved name that cannot be used with email."},
		{"foo@example.arpa", "", "The part after the @-sign is a special-use or reserved name that cannot be used with email."},
		{"foo@127.0.0.1", "", "The part after the @-sign is not valid. It is not within a valid top-level domain."},
		{"a@foo.123", "", "The part after the @-sign is not valid. It is not within a valid top-level domain."},
		{"a@foo.1a", "a@foo.1a", ""},
		{"a@foo.a1", "", "The part after the @-sign is not valid. It is not within a valid top-level domain."},
		{"a(b)@example.com", "", "The email address contains invalid characters before the @-sign: '(', ')'."},
		{"a,bc@example.com", "", "The email address contains invalid characters before the @-sign: ','."},
		{"a:b@example.com", "", "The email address contains invalid characters before the @-sign: ':'."},
		{"a\u200bb@example.com", "", "The email address contains unsafe characters: ZERO WIDTH SPACE."},
		{"a\u200db@example.com", "", "The email address contains unsafe characters: ZERO WIDTH JOINER."},
		{"a\u202eb@example.com", "", "The email address contains unsafe characters: RIGHT-TO-LEFT OVERRIDE."},
		{"a\ufeffb@example.com", "", "The email address contains unsafe characters: ZERO WIDTH NO-BREAK SPACE."},
		{"a\u00a0b@example.com", "", "The email address contains unsafe characters: NO-BREAK SPACE."},
		{"a\u0085b@example.com", "", "The email address contains unsafe characters: U+0085."},
		{"a\u0001b@example.com", "", "The email address contains invalid characters before the @-sign: U+0001."},
		{"a\u0301b@example.com", "áb@example.com", ""},
		{"用户@example.com", "用户@example.com", ""},
		{"a😀@example.com", "a😀@example.com", ""},
		{"a!#$%&'*+-/=?^_`{|}~z@example.com", "a!#$%&'*+-/=?^_`{|}~z@example.com", ""},
		{"foo+bar@EXAMPLE.COM", "foo+bar@example.com", ""},
		{"foo@example.internal", "foo@example.internal", ""},
		{"foo@example.example", "foo@example.example", ""},
	}
	for _, tc := range cases {
		t.Run(tc.email, func(t *testing.T) {
			got, reason := normalizeEmail(tc.email)
			if got != tc.want || reason != tc.reason {
				t.Fatalf("got %q, %q; want %q, %q", got, reason, tc.want, tc.reason)
			}
			if reason != "" {
				w := httptest.NewRecorder()
				body, _ := json.Marshal(map[string]string{"email": tc.email, "password": "sentinel"})
				sessionHandler{}.login(w, withJSONContentType(httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(string(body)))))
				if w.Code != 422 || strings.Contains(w.Body.String(), "sentinel") {
					t.Fatalf("response: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
