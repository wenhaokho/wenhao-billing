package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// cfCall is one request the fake Cloudflare received.
type cfCall struct {
	Method, Path, Auth, ContentType string
	Body                            map[string]any
}

// fakeCF is Cloudflare's DNS record API on httptest. reply answers every
// request (nil: 200 {"success": true}).
type fakeCF struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []cfCall
	reply func(w http.ResponseWriter)
}

func newFakeCF(t *testing.T) *fakeCF {
	t.Helper()
	f := &fakeCF{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.calls = append(f.calls, cfCall{
			Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"), Body: body,
		})
		reply := f.reply
		f.mu.Unlock()
		if reply == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"success": true, "errors": [], "result": {}}`)
			return
		}
		reply(w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCF) client() *Client {
	return &Client{Token: "cf-test-token", BaseURL: f.srv.URL, HTTP: f.srv.Client()}
}

func (f *fakeCF) setReply(reply func(w http.ResponseWriter)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = reply
}

// down makes every request a 500.
func (f *fakeCF) down() {
	f.setReply(func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) })
}

func (f *fakeCF) got() []cfCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cfCall(nil), f.calls...)
}

// ops is each call as Python's test records it: ("suspend"|"restore",
// record id), read from the content sent.
func (f *fakeCF) ops() []string {
	var out []string
	for _, c := range f.got() {
		op := "?"
		switch c.Body["content"] {
		case "maintenance.example.net":
			op = "suspend"
		case "live.example.net":
			op = "restore"
		}
		out = append(out, op+" "+c.Path[strings.LastIndex(c.Path, "/")+1:])
	}
	return out
}

func testTarget() Target {
	return Target{
		ZoneID: "zone_123", RecordID: "record_123", RecordName: "example.com", RecordType: "CNAME",
		LiveContent: "live.example.net", MaintenanceContent: "maintenance.example.net", Proxied: true,
	}
}

// The request is Python's _update_target: PATCH the record with the
// type, name, content and proxied flag, bearer token and JSON.
func TestClientSendsPythonRequest(t *testing.T) {
	t.Parallel()
	cf := newFakeCF(t)
	cl := cf.client()
	tg := testTarget()
	tg.Proxied = false
	if err := cl.Suspend(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	if err := cl.Restore(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	calls := cf.got()
	if len(calls) != 2 {
		t.Fatalf("calls %+v", calls)
	}
	for i, content := range []string{"maintenance.example.net", "live.example.net"} {
		c := calls[i]
		want := map[string]any{"type": "CNAME", "name": "example.com", "content": content, "proxied": false}
		if c.Method != http.MethodPatch || c.Path != "/zones/zone_123/dns_records/record_123" ||
			c.Auth != "Bearer cf-test-token" || c.ContentType != "application/json" || !sameJSON(c.Body, want) {
			t.Errorf("call %d = %+v", i, c)
		}
	}
}

func sameJSON(a, b map[string]any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// Python's _headers: no token is a CloudflareError before any request.
func TestMissingTokenIsCloudflareError(t *testing.T) {
	t.Parallel()
	cf := newFakeCF(t)
	cl := cf.client()
	cl.Token = ""
	for name, call := range map[string]func(context.Context, Target) error{"suspend": cl.Suspend, "restore": cl.Restore} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := call(context.Background(), testTarget())
			if !errors.Is(err, ErrCloudflare) || err.Error() != "cloudflare_api_token is not configured" {
				t.Errorf("err = %v", err)
			}
		})
	}
	t.Cleanup(func() {
		if n := len(cf.got()); n != 0 {
			t.Errorf("%d requests sent without a token", n)
		}
	})
}

// Python's raise_for_status (httpx's sentence) and _parse_error.
func TestCloudflareErrorSentences(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		header map[string]string
		body   string
		want   string // "" is success; "{url}" is the record's URL
	}{
		{"500", 500, nil, "", "Server error '500 Internal Server Error' for url '{url}'\nFor more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/500"},
		{
			"404 with body", 404, nil, `{"success": false, "errors": [{"code": 81044, "message": "Record does not exist."}]}`,
			"Client error '404 Not Found' for url '{url}'\nFor more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/404",
		},
		{
			"redirect", 302,
			map[string]string{"Location": "https://elsewhere.test/x"},
			"",
			"Redirect response '302 Found' for url '{url}'\nRedirect location: 'https://elsewhere.test/x'\nFor more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/302",
		},
		{"code and message", 200, nil, `{"success": false, "errors": [{"code": 1003, "message": "Invalid or missing zone id."}]}`, "1003: Invalid or missing zone id."},
		{"message only", 200, nil, `{"success": false, "errors": [{"message": "bad record"}]}`, "bad record"},
		{"code only", 200, nil, `{"success": false, "errors": [{"code": 9109}]}`, "9109: Cloudflare request failed"},
		{"zero code", 200, nil, `{"success": false, "errors": [{"code": 0, "message": "m"}]}`, "m"},
		{"string error", 200, nil, `{"success": false, "errors": ["plain failure", "second"]}`, "plain failure"},
		{"no errors", 200, nil, `{"success": false, "errors": []}`, "unknown Cloudflare error"},
		{"no success", 200, nil, `{}`, "unknown Cloudflare error"},
		{"null success", 200, nil, `{"success": null, "errors": null}`, "unknown Cloudflare error"},
		{"success", 200, nil, `{"success": true}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cf := newFakeCF(t)
			cf.setReply(func(w http.ResponseWriter) {
				for k, v := range c.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			})
			err := cf.client().Suspend(context.Background(), testTarget())
			if c.want == "" {
				if err != nil {
					t.Errorf("err = %v", err)
				}
				return
			}
			want := strings.ReplaceAll(c.want, "{url}", cf.srv.URL+"/zones/zone_123/dns_records/record_123")
			if !errors.Is(err, ErrCloudflare) || err.Error() != want {
				t.Errorf("err = %q\nwant  %q", err, want)
			}
		})
	}
}
