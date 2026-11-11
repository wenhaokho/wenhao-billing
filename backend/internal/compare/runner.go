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
// never holds a password, a cookie or a token; the two backend URLs are stored
// with any userinfo redacted.
type Report struct {
	Python      string       `json:"python"`
	Go          string       `json:"go"`
	StartedAt   time.Time    `json:"started_at"`
	Cases       []CaseResult `json:"cases"`
	Differences int          `json:"differences"`
	Summary     Summary      `json:"summary"`
	// UnusedAllowed is the allowed entries whose route the run requested but
	// that never applied to an answer (Allowed.Unused): a warning, never a
	// failure, so a dead or misnamed entry is seen.
	UnusedAllowed []UnusedAllowed `json:"unused_allowed"`
}

// Summary counts the run's skipped request variants. A skip is one variant that
// expanded to no request because a discovered id list was empty or its list
// call failed, or a case whose route path itself could not be built; a run with
// any skip is not clean unless --allow-skips is given.
type Summary struct {
	Skipped int `json:"skipped"`
}

// CaseResult is one request's answer pair, or one skipped request variant.
// Difference carries the differences the allowed entries did not cover;
// Classify names the PORT-NOTES note the case is known for (a difference is
// still reported, never hidden). A skipped variant has Skipped set to why no
// request was made, and no status or differences; a case with several query
// variants yields one CaseResult per variant sent, plus one per variant that
// could not be built.
type CaseResult struct {
	ID          string       `json:"id"`
	Request     string       `json:"request,omitempty"`
	Classify    string       `json:"classify,omitempty"`
	Skipped     string       `json:"skipped,omitempty"`
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
	// LedgerMatchesAging adds the cross-check of Go's trial balance (its PN 166
	// case) against Go's AR and AP aging: run it where Go's ledger holds every
	// document (the production copy after the back-post), never before.
	LedgerMatchesAging bool
}

// RunReads signs in to Python (a single POST /auth/login, or nothing when a
// cookie is given) and to Go (the token as a cookie), then checks both
// backends' GET /api/v1/auth/me answers 200, then replays the corpus with GET
// requests only, diffing each pair under the allowed differences. A failed
// sign-in check is a run error (exit 2), so a run where everything 401s can
// never look clean. The two backends' own hosts are set on the Allowed entries
// so a redirect's Location compares equal, and every request failure is a run
// error (exit 2). A request variant whose placeholders cannot be filled is
// recorded as skipped, never dropped, and the case's other variants still run.
func RunReads(ctx context.Context, o ReadOptions) (Report, error) {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	client := readClient(o)
	allowed := o.Allowed.TrackUsage()
	allowed.PythonTZ = o.PythonTZ
	allowed.PythonHost = hostOf(o.PythonURL)
	allowed.GoHost = hostOf(o.GoURL)

	report := Report{Python: redactURL(o.PythonURL), Go: redactURL(o.GoURL), StartedAt: now().UTC(), UnusedAllowed: []UnusedAllowed{}}

	session, err := pythonSignIn(ctx, client, o)
	if err != nil {
		return report, err
	}
	if err := signInCheck(ctx, client, o.PythonURL, session, "python"); err != nil {
		return report, err
	}
	if err := signInCheck(ctx, client, o.GoURL, o.GoToken, "go"); err != nil {
		return report, err
	}
	today := now().UTC().Format("2006-01-02")

	discovered := map[string][]string{}
	// The Go answers the aging cross-check reads: the first trial balance of
	// a PN 166 case (and its report index), and the first AR and AP aging.
	ledger := ledgerAnswers{tbCase: -1}
	for _, cs := range o.Corpus.Cases {
		route := "GET " + routePath(cs.Route)
		targets, skips := expandCase(cs, discovered, today)
		for _, target := range targets {
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
			if strings.HasPrefix(cs.Classify, ledgerClassify) {
				// Go's ledger report cannot be compared with Python's here; it
				// is checked against itself, and a failure is a difference.
				diffs = append(diffs, withClassify(selfCheck(routePath(cs.Route), gov), cs.Classify)...)
			}
			ledger.record(routePath(cs.Route), cs.Classify, gov, len(report.Cases))
			report.Cases = append(report.Cases, CaseResult{
				ID: cs.ID, Request: "GET " + target, Classify: cs.Classify,
				Python: py.Status, Go: gov.Status, Differences: diffs,
			})
			report.Differences += len(diffs)
		}
		// A variant that cannot be built is recorded, never dropped, and the
		// case's other variants still ran above.
		for _, s := range skips {
			report.Cases = append(report.Cases, CaseResult{
				ID: cs.ID, Request: s.request(route), Classify: cs.Classify,
				Skipped: s.message(),
			})
			report.Summary.Skipped++
		}
	}
	if o.LedgerMatchesAging {
		if ledger.tbCase < 0 {
			return report, fmt.Errorf("the ledger-vs-aging check needs a %s trial-balance case in the corpus", ledgerClassify)
		}
		tb := &report.Cases[ledger.tbCase]
		d := withClassify(agingCheck(ledger.tb, ledger.ar, ledger.ap, ledger.haveAR, ledger.haveAP), tb.Classify)
		tb.Differences = append(tb.Differences, d...)
		report.Differences += len(d)
	}
	report.UnusedAllowed = allowed.Unused()
	return report, nil
}

// ledgerAnswers keeps the Go answers the aging cross-check needs.
type ledgerAnswers struct {
	tb, ar, ap     Response
	tbCase         int
	haveAR, haveAP bool
}

// record keeps the first answer of each report the cross-check reads.
func (l *ledgerAnswers) record(path, classify string, gov Response, index int) {
	switch {
	case path == trialBalanceRoute && strings.HasPrefix(classify, ledgerClassify) && l.tbCase < 0:
		l.tb, l.tbCase = gov, index
	case path == arAgingRoute && !l.haveAR:
		l.ar, l.haveAR = gov, true
	case path == apAgingRoute && !l.haveAP:
		l.ap, l.haveAP = gov, true
	}
}

// withClassify tags self-check differences with the case's note.
func withClassify(diffs []Difference, note string) []Difference {
	for i := range diffs {
		diffs[i].Classify = note
	}
	return diffs
}

// signInCheck asks a backend GET /api/v1/auth/me and refuses the run unless it
// answers 200. name is "python" or "go". The response body is never printed, so
// a sign-in failure can never leak an error page.
func signInCheck(ctx context.Context, client *http.Client, base, cookie, name string) error {
	resp, err := get(ctx, client, base, "/api/v1/auth/me", cookie)
	if err != nil {
		return fmt.Errorf("sign-in check failed on %s: %w", name, err)
	}
	if resp.Status != http.StatusOK {
		return fmt.Errorf("sign-in check failed on %s: %d", name, resp.Status)
	}
	return nil
}

// ExitCode maps a run to the process exit code: 0 clean, 1 differences or
// skips, 2 on error. A run with skipped cases is not clean unless allowSkips is
// set, because a skip means a whole area of the corpus was never compared.
func ExitCode(report Report, err error, allowSkips bool) int {
	switch {
	case err != nil:
		return 2
	case report.Differences > 0:
		return 1
	case report.Summary.Skipped > 0 && !allowSkips:
		return 1
	default:
		return 0
	}
}

// readClient is the run's HTTP client. It never follows a redirect, because the
// diff compares the Location itself (the two backends' own hosts are folded
// away, decision 1); the policy is set even on a caller's client, and the
// client is copied, never mutated. The default client's transport has Proxy
// cleared, so a credential is never sent through an ambient HTTP proxy.
func readClient(o ReadOptions) *http.Client {
	client := o.Client
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client = &http.Client{Timeout: 30 * time.Second, Transport: transport}
	} else {
		clone := *client
		client = &clone
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

// redactURL is a backend URL with any userinfo password folded away, so the
// report never carries one.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Redacted()
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

// caseSkip is one request variant expandCase could not build: the variant's
// query (empty for the bare route) and the {name} that had no value.
// pathMissing marks a skip of the whole case, because the route's own path
// holds a {name} with no value — every variant is then unbuildable.
type caseSkip struct {
	query       string
	missing     string
	pathMissing bool
}

// message is the report's reason. A case-level skip names the list that was
// empty or failed; a query-variant skip names the variant and the placeholder
// it needed, so the report shows exactly which request was not sent.
func (s caseSkip) message() string {
	if s.pathMissing {
		return fmt.Sprintf("no {%s}: list empty or failed", s.missing)
	}
	return fmt.Sprintf("variant %s: no {%s}", s.query, s.missing)
}

// request is the route a skipped variant would have sent, with its query when
// the variant has one.
func (s caseSkip) request(route string) string {
	if s.query == "" {
		return route
	}
	return route + "?" + s.query
}

// expandCase is the case's requests: one per query variant that expands (the
// bare route when the case names no query), with every {name} filled from the
// discovered ids and {today} from the run's date. A query variant whose
// placeholder has no value — its list was empty or its list call failed — is
// reported as a skip while the case's other variants still run; a route path
// whose placeholder has no value skips the whole case, because no variant can
// be built. Nothing is dropped silently.
func expandCase(cs Case, discovered map[string][]string, today string) ([]string, []caseSkip) {
	queries := cs.Queries
	if len(queries) == 0 {
		queries = []string{""}
	}
	paths, pathMissing := expandPlaceholders(routePath(cs.Route), discovered, today, url.PathEscape)
	if pathMissing != "" {
		return nil, []caseSkip{{missing: pathMissing, pathMissing: true}}
	}
	var out []string
	var skips []caseSkip
	seen := map[string]bool{}
	for _, q := range queries {
		qs, qMissing := expandPlaceholders(q, discovered, today, url.QueryEscape)
		if qMissing != "" {
			skips = append(skips, caseSkip{query: q, missing: qMissing})
			continue
		}
		for _, p := range paths {
			for _, query := range qs {
				target := p
				if query != "" {
					target += "?" + query
				}
				if !seen[target] {
					seen[target] = true
					out = append(out, target)
				}
			}
		}
	}
	return out, skips
}

// expandPlaceholders fills every {name} in s with escape applied to each value:
// url.PathEscape for a path, so an id with a slash cannot change the route, and
// url.QueryEscape for a query, so an id with &, = or + cannot break out of its
// value into another parameter. A name with no discovered value (an empty list,
// say) yields no results and returns that name, so the caller records a skip
// instead of dropping the request.
func expandPlaceholders(s string, discovered map[string][]string, today string, escape func(string) string) ([]string, string) {
	names := placeholders(s)
	if len(names) == 0 {
		return []string{s}, ""
	}
	out := []string{s}
	for _, name := range names {
		values := discovered[name]
		if name == todayToken {
			values = []string{today}
		}
		if len(values) == 0 {
			return nil, name
		}
		values = pickIDs(values)
		var next []string
		for _, o := range out {
			for _, v := range values {
				next = append(next, strings.ReplaceAll(o, "{"+name+"}", escape(v)))
			}
		}
		out = next
	}
	return out, ""
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
