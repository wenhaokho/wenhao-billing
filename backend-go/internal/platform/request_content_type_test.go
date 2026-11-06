package platform

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestJSONMediaType(t *testing.T) {
	for _, tc := range []struct {
		media string
		want  bool
	}{
		{"", false}, {"application/json", true}, {"Application/JSON; charset=utf-8", true}, {"application/problem+json", true}, {"application/+json", true}, {"application/foo+json; bad", true}, {"application/foo/bar+json", false}, {"application//foo+json", false}, {"application/foo+json/", false}, {"application/foo+json, text/plain", false}, {"text/plain", false}, {"garbage", false}, {"text/json", false},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"password":"secret"}`))
		if tc.media != "" {
			r.Header.Set("Content-Type", tc.media)
		}
		if got := requestJSONMediaType(r); got != tc.want {
			t.Errorf("%q: got %v", tc.media, got)
		}
		body, ok := readItemBody(httptest.NewRecorder(), r)
		if !ok {
			t.Fatal("body read failed")
		}
		if !tc.want && string(body) != `"[REDACTED]"` {
			t.Errorf("%q: leaked body %s", tc.media, body)
		}
	}
}
