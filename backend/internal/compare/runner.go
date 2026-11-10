package compare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Report is one `compare reads` run's result, written as the --out JSON. It
// never holds a password, a cookie or a token.
type Report struct {
	Python      string       `json:"python"`
	Go          string       `json:"go"`
	StartedAt   time.Time    `json:"started_at"`
	Cases       []CaseResult `json:"cases"`
	Differences int          `json:"differences"`
}

// CaseResult is one request's answer pair. Difference carries the differences
// the allowed entries did not cover; Classify names the PORT-NOTES note the
// case is known for (a difference is still reported, never hidden).
type CaseResult struct {
	ID          string       `json:"id"`
	Request     string       `json:"request"`
	Classify    string       `json:"classify,omitempty"`
	Python      int          `json:"python_status"`
	Go          int          `json:"go_status"`
	Differences []Difference `json:"differences,omitempty"`
}

// ReadOptions is one read run's inputs. Every credential arrives already read
// from stdin; RunReads never logs one.
type ReadOptions struct {
	PythonURL      string
	GoURL          string
	PythonEmail    string
	PythonPassword string
	// PythonCookie, when set, is a ready billing_session value and no login
	// POST is sent at all.
	PythonCookie string
	// GoToken is `api issue-session` output, sent as the billing_session
	// cookie. The runner never POSTs to Go.
	GoToken string
	Corpus  Corpus
	Allowed Allowed
	// PythonTZ reads a naive Python timestamp; nil is UTC.
	PythonTZ *time.Location
	Client   *http.Client
	// Now is the run's clock, for {today} and the report's timestamp.
	Now func() time.Time
}

// RunReads signs in to Python (a single POST /auth/login, or nothing when a
// cookie is given) and to Go (the token as a cookie), then replays the corpus
// with GET requests only, diffing each pair under the allowed differences.
// The two backends' own hosts are set on the Allowed entries so a redirect's
// Location compares equal, and every request failure is a run error (exit 2).
func RunReads(ctx context.Context, o ReadOptions) (Report, error) {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	client := readClient(o)
	allowed := o.Allowed
	allowed.PythonTZ = o.PythonTZ
	allowed.PythonHost = hostOf(o.PythonURL)
	allowed.GoHost = hostOf(o.GoURL)

	report := Report{Python: o.PythonURL, Go: o.GoURL, StartedAt: now().UTC()}

	session, err := pythonSignIn(ctx, client, o)
	if err != nil {
		return report, err
	}
	today := now().UTC().Format("2006-01-02")

	discovered := map[string][]string{}
	for _, cs := range o.Corpus.Cases {
		route := "GET " + routePath(cs.Route)
		for _, target := range expandCase(cs, discovered, today) {
			py, err := get(ctx, client, o.PythonURL, target, session)
			if err != nil {
				return report, fmt.Errorf("case %s: python: %w", cs.ID, err)
			}
			gov, err := get(ctx, client, o.GoURL, target, o.GoToken)
			if err != nil {
				return report, fmt.Errorf("case %s: go: %w", cs.ID, err)
			}
			for _, d := range cs.Discover {
				discoverIDs(discovered, py.Body, d)
				discoverIDs(discovered, gov.Body, d)
			}
			diffs := Diff(route, py, gov, allowed)
			report.Cases = append(report.Cases, CaseResult{
				ID: cs.ID, Request: "GET " + target, Classify: cs.Classify,
				Python: py.Status, Go: gov.Status, Differences: diffs,
			})
			report.Differences += len(diffs)
		}
	}
	return report, nil
}

// ExitCode maps a run to the process exit code: 0 clean, 1 differences, 2 on
// error.
func ExitCode(report Report, err error) int {
	switch {
	case err != nil:
		return 2
	case report.Differences > 0:
		return 1
	default:
		return 0
	}
}

// readClient is the run's HTTP client: it never follows a redirect, because
// the diff compares the Location itself (the two backends' own hosts are
// folded away, decision 1). A caller's client is copied, never mutated.
func readClient(o ReadOptions) *http.Client {
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	if client.CheckRedirect == nil {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client
}

// pythonSignIn returns the Python session cookie value. With PythonCookie set
// it sends no request; otherwise it sends the one and only non-GET the runner
// makes: POST /auth/login, with the email and the password from stdin. A
// failed login is a run error, and the error never names the password.
func pythonSignIn(ctx context.Context, client *http.Client, o ReadOptions) (string, error) {
	if o.PythonCookie != "" {
		return o.PythonCookie, nil
	}
	if o.PythonPassword == "" {
		return "", fmt.Errorf("python sign-in: no password or cookie (--python-password-stdin or --python-cookie-stdin)")
	}
	body, err := json.Marshal(map[string]any{"email": o.PythonEmail, "password": o.PythonPassword, "remember": false})
	if err != nil {
		return "", fmt.Errorf("python sign-in: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(o.PythonURL, "/api/v1/auth/login"), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("python sign-in: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("python sign-in: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("python sign-in: status %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == platform.SessionCookie && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("python sign-in: the answer carried no %s cookie", platform.SessionCookie)
}

// get sends one GET, with the cookie in the billing_session header. It is the
// only request the runner makes after sign-in.
func get(ctx context.Context, client *http.Client, base, target, cookie string) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(base, target), nil)
	if err != nil {
		return Response{}, err
	}
	if cookie != "" {
		req.Header.Set("Cookie", platform.SessionCookie+"="+cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// endpoint joins a base URL and an absolute path.
func endpoint(base, path string) string { return strings.TrimRight(base, "/") + path }

// hostOf is a URL's host (with its port), the name a redirect on that backend
// is compared against.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// expandCase is the case's requests: one per query variant (the bare route
// when it names none), with every {name} filled from the discovered ids and
// {today} from the run's date. A detail case whose list was empty expands to
// no request.
func expandCase(cs Case, discovered map[string][]string, today string) []string {
	queries := cs.Queries
	if len(queries) == 0 {
		queries = []string{""}
	}
	paths := expandPlaceholders(routePath(cs.Route), discovered, today)
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		for _, q := range queries {
			for _, qs := range expandPlaceholders(q, discovered, today) {
				target := p
				if qs != "" {
					target += "?" + qs
				}
				if !seen[target] {
					seen[target] = true
					out = append(out, target)
				}
			}
		}
	}
	return out
}

// expandPlaceholders fills every {name} in s. A name with no discovered value
// (an empty list, say) yields no results, so the case is skipped.
func expandPlaceholders(s string, discovered map[string][]string, today string) []string {
	names := placeholders(s)
	if len(names) == 0 {
		return []string{s}
	}
	out := []string{s}
	for _, name := range names {
		values := discovered[name]
		if name == todayToken {
			values = []string{today}
		}
		if len(values) == 0 {
			return nil
		}
		values = pickIDs(values)
		var next []string
		for _, o := range out {
			for _, v := range values {
				next = append(next, strings.ReplaceAll(o, "{"+name+"}", v))
			}
		}
		out = next
	}
	return out
}

// maxDiscoveredIDs is how many ids of one list the runner follows: the first,
// the last and a middle element, never a hard-coded path id.
const maxDiscoveredIDs = 3

// pickIDs is the first, last and middle of ids (deduplicated), up to
// maxDiscoveredIDs of them. Fewer ids are all kept.
func pickIDs(ids []string) []string {
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) <= maxDiscoveredIDs {
		return unique
	}
	return []string{unique[0], unique[len(unique)-1], unique[len(unique)/2]}
}

// discoverIDs collects one list response's element field into discovered,
// keeping order and dropping duplicates. It reads the response whether the
// list is the whole body or one of its arrays, so a wrapped list still works.
func discoverIDs(discovered map[string][]string, body []byte, src IDSource) {
	field := src.Field
	if field == "" {
		field = src.Name
	}
	for _, v := range listValues(body) {
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id := idString(obj[field])
		if id == "" {
			continue
		}
		ids := discovered[src.Name]
		found := false
		for _, have := range ids {
			if have == id {
				found = true
				break
			}
		}
		if !found {
			discovered[src.Name] = append(ids, id)
		}
	}
}

// listValues is the elements of a list response: the body itself when it is an
// array, or the first array value of an object body.
func listValues(body []byte) []any {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		for _, key := range []string{"items", "rows", "data", "results"} {
			if arr, ok := x[key].([]any); ok {
				return arr
			}
		}
	}
	return nil
}

// idString renders a JSON id (a number or a string) as the string a path
// segment takes. An object or an array is not an id.
func idString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return ""
	}
}
