// Package hosting suspends and restores hosting items through Cloudflare
// (Python's app/services/cloudflare.py, hosting.py and the daily
// hosting_enforcement job).
package hosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ErrCloudflare is matched (errors.Is) by every Cloudflare failure; the
// error's text is Python's (stored as the target's last_error).
var ErrCloudflare = errors.New("cloudflare error")

type cfError struct{ msg string }

func (e cfError) Error() string        { return e.msg }
func (e cfError) Is(target error) bool { return target == ErrCloudflare }

// Target is the DNS record a hosting item flips between its live and
// maintenance content.
type Target struct {
	ZoneID, RecordID, RecordName, RecordType string
	LiveContent, MaintenanceContent          string
	Proxied                                  bool
}

// Client is Cloudflare's DNS record API.
type Client struct {
	// Token is CLOUDFLARE_API_TOKEN; empty fails every call.
	Token string
	// BaseURL is CLOUDFLARE_API_BASE_URL.
	BaseURL string
	// HTTP carries the timeout (nil: http.DefaultClient). Redirects are never
	// followed (httpx.s default): a 3xx fails with raise_for_status.s sentence.
	HTTP *http.Client
}

// NewClient is the client cfg configures (CLOUDFLARE_*).
func NewClient(cfg platform.Config) *Client {
	return &Client{
		Token: cfg.CloudflareAPIToken, BaseURL: cfg.CloudflareAPIBaseURL,
		HTTP: &http.Client{Timeout: cfg.CloudflareTimeout},
	}
}

// Suspend points the record at its maintenance content (Python's
// suspend_target).
func (c *Client) Suspend(ctx context.Context, t Target) error {
	return c.update(ctx, t, t.MaintenanceContent)
}

// Restore points the record back at its live content (Python's
// restore_target).
func (c *Client) Restore(ctx context.Context, t Target) error {
	return c.update(ctx, t, t.LiveContent)
}

// update is Python's _update_target: PATCH the record, then httpx's
// raise_for_status, then the body's success flag. Every failure is
// ErrCloudflare; a transport or JSON failure carries Go's text, not
// httpx's.
func (c *Client) update(ctx context.Context, t Target, content string) error {
	payload, err := json.Marshal(map[string]any{
		"type": t.RecordType, "name": t.RecordName, "content": content, "proxied": t.Proxied,
	})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", c.BaseURL, t.ZoneID, t.RecordID)
	if c.Token == "" {
		return cfError{"cloudflare_api_token is not configured"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return cfError{fmt.Sprintf("cloudflare request failed: %v", err)}
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	var hc http.Client
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return cfError{fmt.Sprintf("cloudflare request failed: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if msg := statusError(resp, url); msg != "" {
		return cfError{msg}
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var body any
	if err := dec.Decode(&body); err != nil {
		return cfError{fmt.Sprintf("cloudflare response is not JSON: %v", err)}
	}
	obj, ok := body.(map[string]any)
	if !ok {
		return cfError{"cloudflare response is not a JSON object"}
	}
	if !truthy(obj["success"]) {
		return cfError{parseError(obj)}
	}
	return nil
}

// statusError is httpx's raise_for_status sentence for a non-2xx response,
// "" for a 2xx.
func statusError(resp *http.Response, url string) string {
	code := resp.StatusCode
	if code >= 200 && code < 300 {
		return ""
	}
	kind := map[int]string{1: "Informational response", 3: "Redirect response", 4: "Client error", 5: "Server error"}[code/100]
	if kind == "" {
		kind = "Invalid status code"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s '%d %s' for url '%s'\n", kind, code, http.StatusText(code), url)
	switch code {
	case 301, 302, 303, 307, 308:
		if loc := resp.Header.Get("Location"); loc != "" {
			fmt.Fprintf(&b, "Redirect location: '%s'\n", loc)
		}
	}
	fmt.Fprintf(&b, "For more information check: https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/%d", code)
	return b.String()
}

// parseError is Python's _parse_error.
func parseError(body map[string]any) string {
	errs, _ := body["errors"].([]any)
	if len(errs) == 0 {
		return "unknown Cloudflare error"
	}
	first, ok := errs[0].(map[string]any)
	if !ok {
		return pyStr(errs[0])
	}
	message := first["message"]
	if !truthy(message) {
		message = "Cloudflare request failed"
	}
	if code := first["code"]; truthy(code) {
		return pyStr(code) + ": " + pyStr(message)
	}
	return pyStr(message)
}

// truthy is Python's truth value of a decoded JSON value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

// pyStr is str() of a decoded JSON scalar; anything else is its JSON.
func pyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
