package platform

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestItemRoutesRequireSession(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/items", ""}, {"POST", "/api/v1/items", `{}`},
		{"GET", "/api/v1/items/00000000-0000-0000-0000-000000000001", ""},
		{"PATCH", "/api/v1/items/00000000-0000-0000-0000-000000000001", `{}`},
		{"DELETE", "/api/v1/items/00000000-0000-0000-0000-000000000001", ""},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestItemReadUnauthenticated(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/items", "/api/v1/items/bad"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}
