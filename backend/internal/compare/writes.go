package compare

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gopkg.in/yaml.v2"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// WritesScenarios is the write comparison's ordered list of scenarios.
type WritesScenarios struct {
	Scenarios []WritesScenario `yaml:"scenarios"`
}

// WritesScenario is one ordered write flow. ID names it in the report, and
// Classify names the PORT-NOTES note a difference under it belongs to (a
// classification is never an allowance). Tables are the tables the flow
// touches; the row comparison reads them after the whole run.
type WritesScenario struct {
	ID       string      `yaml:"id"`
	Classify string      `yaml:"classify"`
	Tables   []string    `yaml:"tables"`
	Steps    []WriteStep `yaml:"steps"`
}

// WriteStep is one request both backends send. Path is sent with each
// backend's own id for every {name}. Extract maps a placeholder name to a
// JSONPath in the response; the Python id is stored beside the Go id and
// later steps use each backend's own id. Sign sends the body's HMAC-SHA256
// under WEBHOOK_HMAC_SECRET in the X-Signature header.
type WriteStep struct {
	ID      string            `yaml:"id"`
	Method  string            `yaml:"method"`
	Path    string            `yaml:"path"`
	Body    any               `yaml:"body"`
	Extract map[string]string `yaml:"extract"`
	Sign    bool              `yaml:"sign"`
}

// WritesReport is one `compare writes` run's result. It never holds a DSN or a
// password: the two URLs are stored with any userinfo redacted, and a row is
// the canonical row text, not a connection string. Differences counts the
// row differences and every step's response differences.
type WritesReport struct {
	Python      string                 `json:"python"`
	Go          string                 `json:"go"`
	StartedAt   time.Time              `json:"started_at"`
	Scenarios   []WritesScenarioResult `json:"scenarios"`
	RowDiffs    []RowDiff              `json:"row_diffs,omitempty"`
	Differences int                    `json:"differences"`
}

// WritesScenarioResult is one scenario's steps. Classify names the note the
// scenario is known for; a row difference is still reported, never hidden.
type WritesScenarioResult struct {
	ID       string             `json:"id"`
	Classify string             `json:"classify,omitempty"`
	Steps    []WritesStepResult `json:"steps"`
}

// WritesStepResult is one request's status pair and the differences between
// the two answers. Path is the step's template path, not the expanded one, so
// a report never carries a backend's id. The answers are compared with the
// read runner's Diff after the run, once every id is mapped, so a Python id in
// the body compares as its Go id.
type WritesStepResult struct {
	ID          string       `json:"id"`
	Method      string       `json:"method"`
	Path        string       `json:"path"`
	Python      int          `json:"python_status"`
	Go          int          `json:"go_status"`
	Differences []Difference `json:"differences,omitempty"`
}

// WritesOptions is one write run's inputs. The DSNs arrive as arguments and
// are never logged, put in an error or written to the report.
type WritesOptions struct {
	PythonURL      string
	GoURL          string
	PythonDB       string
	GoDB           string
	PythonEmail    string
	PythonPassword string
	// PythonCookie, when set, is a ready billing_session value and no login
	// POST is sent at all.
	PythonCookie string
	// GoToken is `api issue-session` output, sent as the billing_session
	// cookie.
	GoToken   string
	Scenarios WritesScenarios
	// Allowed is the allowed-diffs entries the step answers are compared
	// under; the zero value allows nothing.
	Allowed Allowed
	Client  *http.Client
	// Now is the run's clock, for the report's timestamp.
	Now func() time.Time
	// Env reads the runner's own process environment; nil means os.Getenv.
	// It is used for the RESEND_API_KEY guard and the WEBHOOK_HMAC_SECRET
	// signature. It cannot see the backends' environments.
	Env func(string) string
}

// idPair is one placeholder's id on each backend.
type idPair struct {
	python string
	goID   string
}

// stepAnswers is one sent step's two answers, kept for the comparison after
// the run.
type stepAnswers struct {
	scenario, step int
	route          string
	py, gov        Response
}

// RunWrites runs the write scenarios against both backends, then compares the
// rows they touched and every step's answers. It refuses to run unless both
// database hosts and both backend URLs are local and the runner's own
// environment has no RESEND_API_KEY. A non-2xx answer is a hard failure: the
// run stops there, so later steps never run against a broken flow.
//
// Before the first step it snapshots every touched table on both databases,
// so only rows the run created, changed or deleted are compared. Each
// extracted id is stored under the table its JSON key names; the row
// comparison derives the pairs no response named, and the step answers are
// compared last, with every Python id mapped to its Go id.
//
// The DSNs are never logged, never returned in an error and never written to
// the report.
func RunWrites(ctx context.Context, o WritesOptions) (WritesReport, error) {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	env := o.Env
	if env == nil {
		env = os.Getenv
	}
	o.Env = env
	report := WritesReport{Python: redactURL(o.PythonURL), Go: redactURL(o.GoURL), StartedAt: now().UTC()}

	if env("RESEND_API_KEY") != "" {
		return report, errors.New("compare writes: RESEND_API_KEY is set in this runner's own environment; refusing to run. " +
			"The runner cannot see the backends' environments: start both backends with RESEND_API_KEY and CLOUDFLARE_API_TOKEN unset")
	}
	if err := checkLocalDB(o.PythonDB, "python-db"); err != nil {
		return report, err
	}
	if err := checkLocalDB(o.GoDB, "go-db"); err != nil {
		return report, err
	}
	if err := checkLocalURL(o.PythonURL, "python"); err != nil {
		return report, err
	}
	if err := checkLocalURL(o.GoURL, "go"); err != nil {
		return report, err
	}

	client := readClient(ReadOptions{Client: o.Client})
	session, err := pythonSignIn(ctx, client, ReadOptions{
		PythonURL: o.PythonURL, PythonEmail: o.PythonEmail,
		PythonPassword: o.PythonPassword, PythonCookie: o.PythonCookie,
	})
	if err != nil {
		return report, err
	}
	if err := signInCheck(ctx, client, o.PythonURL, session, "python"); err != nil {
		return report, err
	}
	if err := signInCheck(ctx, client, o.GoURL, o.GoToken, "go"); err != nil {
		return report, err
	}

	pyDB, err := db.Open(ctx, db.PoolConfig{URL: o.PythonDB})
	if err != nil {
		return report, fmt.Errorf("compare writes: open python-db: %w", redactErr(err, o.PythonDB))
	}
	defer pyDB.Close()
	goDB, err := db.Open(ctx, db.PoolConfig{URL: o.GoDB})
	if err != nil {
		return report, fmt.Errorf("compare writes: open go-db: %w", redactErr(err, o.GoDB))
	}
	defer goDB.Close()
	dbErr := func(what string, err error) error {
		return fmt.Errorf("compare writes: %s: %w", what, redactErr(redactErr(err, o.PythonDB), o.GoDB))
	}

	schema, err := LoadSchema(ctx, pyDB)
	if err != nil {
		return report, dbErr("reading the schema", err)
	}
	tables := collectTables(o.Scenarios.Scenarios)
	pySnap, err := TakeSnapshot(ctx, pyDB, schema, tables)
	if err != nil {
		return report, dbErr("python snapshot", err)
	}
	goSnap, err := TakeSnapshot(ctx, goDB, schema, tables)
	if err != nil {
		return report, dbErr("go snapshot", err)
	}

	r := writesRun{
		client: client, o: o, session: session, schema: schema,
		extracted: map[string]idPair{}, maps: IDMaps{},
	}
	for i, sc := range o.Scenarios.Scenarios {
		result, err := r.scenario(ctx, i, sc)
		report.Scenarios = append(report.Scenarios, result)
		if err != nil {
			return report, err
		}
	}

	diffs, err := Rows(ctx, RowsInput{
		Python: pyDB, Go: goDB, Schema: schema, Tables: tables, Maps: r.maps,
		PythonSnapshot: pySnap, GoSnapshot: goSnap,
	})
	if err != nil {
		return report, dbErr("row comparison", err)
	}
	report.RowDiffs = diffs
	report.Differences = len(diffs)

	c := newRowComparer(schema, r.maps)
	allowed := o.Allowed
	allowed.PythonHost = hostOf(o.PythonURL)
	allowed.GoHost = hostOf(o.GoURL)
	for _, a := range r.answers {
		py := Response{Status: a.py.Status, Header: a.py.Header, Body: c.normalizeBody(a.py.Body, true)}
		gov := Response{Status: a.gov.Status, Header: a.gov.Header, Body: c.normalizeBody(a.gov.Body, false)}
		d := Diff(a.route, py, gov, allowed)
		report.Scenarios[a.scenario].Steps[a.step].Differences = d
		report.Differences += len(d)
	}
	return report, nil
}

// WritesExitCode maps a run to the process exit code: 0 clean, 1 differences,
// 2 on error.
func WritesExitCode(report WritesReport, err error) int {
	switch {
	case err != nil:
		return 2
	case report.Differences > 0:
		return 1
	default:
		return 0
	}
}

// writesRun is one run's state across scenarios: the placeholders extracted
// so far, the entity-keyed id maps and the answers to compare afterwards.
type writesRun struct {
	client    *http.Client
	o         WritesOptions
	session   string
	schema    Schema
	extracted map[string]idPair
	maps      IDMaps
	answers   []stepAnswers
}

// scenario runs one scenario's steps in order. It returns the result so far
// and the first hard failure, so a report still shows the steps that ran.
func (r *writesRun) scenario(ctx context.Context, index int, sc WritesScenario) (WritesScenarioResult, error) {
	o := r.o
	result := WritesScenarioResult{ID: sc.ID, Classify: sc.Classify}
	for _, step := range sc.Steps {
		fail := func(format string, args ...any) error {
			return fmt.Errorf("scenario %s: step %s: "+format, append([]any{sc.ID, step.ID}, args...)...)
		}
		pyPath, err := expandPath(step.Path, r.extracted, false)
		if err != nil {
			return result, fail("%w", err)
		}
		goPath, err := expandPath(step.Path, r.extracted, true)
		if err != nil {
			return result, fail("%w", err)
		}
		pyBody, err := buildBody(step.Body, r.extracted, false)
		if err != nil {
			return result, fail("%w", err)
		}
		goBody, err := buildBody(step.Body, r.extracted, true)
		if err != nil {
			return result, fail("%w", err)
		}

		pyResp, err := send(ctx, r.client, o, o.PythonURL, step.Method, pyPath, pyBody, r.session, step.Sign)
		if err != nil {
			return result, fail("python: %w", redactErr(err, o.PythonURL))
		}
		goResp, err := send(ctx, r.client, o, o.GoURL, step.Method, goPath, goBody, o.GoToken, step.Sign)
		if err != nil {
			return result, fail("go: %w", redactErr(err, o.GoURL))
		}
		result.Steps = append(result.Steps, WritesStepResult{
			ID: step.ID, Method: step.Method, Path: step.Path,
			Python: pyResp.Status, Go: goResp.Status,
		})
		r.answers = append(r.answers, stepAnswers{
			scenario: index, step: len(result.Steps) - 1,
			route: strings.ToUpper(step.Method) + " " + step.Path, py: pyResp, gov: goResp,
		})
		if pyResp.Status < 200 || pyResp.Status >= 300 {
			return result, fail("python status %d", pyResp.Status)
		}
		if goResp.Status < 200 || goResp.Status >= 300 {
			return result, fail("go status %d", goResp.Status)
		}
		for _, name := range sortedExtracts(step.Extract) {
			path := step.Extract[name]
			table, ok := r.schema.EntityForKey(lastKey(path))
			if !ok {
				return result, fail("extract %s: %q names no single table's id", name, path)
			}
			pyID, err := extractID(pyResp.Body, path)
			if err != nil {
				return result, fail("python %s: %w", name, err)
			}
			goID, err := extractID(goResp.Body, path)
			if err != nil {
				return result, fail("go %s: %w", name, err)
			}
			r.extracted[name] = idPair{python: pyID, goID: goID}
			if err := r.maps.Set(table, pyID, goID); err != nil {
				return result, fail("extract %s: %w", name, err)
			}
		}
	}
	return result, nil
}

// sortedExtracts is a step's extract names in order, so a run is repeatable.
func sortedExtracts(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// lastKey is a JSONPath's last object key: "$.items[0].invoice_id" is
// "invoice_id". It names the table the extracted id belongs to.
func lastKey(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return ""
	}
	key := path[i+1:]
	if j := strings.IndexByte(key, '['); j >= 0 {
		key = key[:j]
	}
	return key
}

// send sends one request to one backend. The response body is never put in an
// error: a 4xx or 5xx body may carry a secret or a stack trace.
func send(ctx context.Context, client *http.Client, o WritesOptions, base, method, path string, body []byte, cookie string, sign bool) (Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint(base, path), r)
	if err != nil {
		return Response{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", platform.SessionCookie+"="+cookie)
	}
	if sign {
		secret := o.Env("WEBHOOK_HMAC_SECRET")
		if secret == "" {
			return Response{}, errors.New("WEBHOOK_HMAC_SECRET is not set")
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, err
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: b}, nil
}

// expandPath fills each {name} in a path with that backend's id. The name must
// be a placeholder an earlier step extracted; a missing one is a hard error.
func expandPath(path string, extracted map[string]idPair, goSide bool) (string, error) {
	var out strings.Builder
	last := 0
	for _, m := range placeholder.FindAllStringSubmatchIndex(path, -1) {
		out.WriteString(path[last:m[0]])
		name := path[m[2]:m[3]]
		pair, ok := extracted[name]
		if !ok {
			return "", fmt.Errorf("path %q: no {%s} extracted yet", path, name)
		}
		if goSide {
			out.WriteString(pair.goID)
		} else {
			out.WriteString(pair.python)
		}
		last = m[1]
	}
	out.WriteString(path[last:])
	return out.String(), nil
}

// buildBody replaces every {name} in a JSON body with that backend's id and
// marshals it. A nil body stays nil.
func buildBody(body any, extracted map[string]idPair, goSide bool) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	replaced, err := replacePlaceholders(body, extracted, goSide)
	if err != nil {
		return nil, err
	}
	return json.Marshal(replaced)
}

// replacePlaceholders walks a YAML-decoded value and replaces every {name} in
// a string with the backend's id. A name with no extracted value is an error.
func replacePlaceholders(v any, extracted map[string]idPair, goSide bool) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			rv, err := replacePlaceholders(val, extracted, goSide)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			rv, err := replacePlaceholders(val, extracted, goSide)
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(k)] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			rv, err := replacePlaceholders(val, extracted, goSide)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case string:
		return expandString(x, extracted, goSide)
	default:
		return v, nil
	}
}

// expandString replaces each {name} in a string with the backend's id.
func expandString(s string, extracted map[string]idPair, goSide bool) (string, error) {
	var out strings.Builder
	last := 0
	for _, m := range placeholder.FindAllStringSubmatchIndex(s, -1) {
		out.WriteString(s[last:m[0]])
		name := s[m[2]:m[3]]
		pair, ok := extracted[name]
		if !ok {
			return "", fmt.Errorf("body %q: no {%s} extracted yet", s, name)
		}
		if goSide {
			out.WriteString(pair.goID)
		} else {
			out.WriteString(pair.python)
		}
		last = m[1]
	}
	out.WriteString(s[last:])
	return out.String(), nil
}

// extractID reads one JSONPath from a response body. The id is a string or a
// number; a missing or null value is an error.
func extractID(body []byte, path string) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	got, err := jsonPath(v, path)
	if err != nil {
		return "", err
	}
	id := idString(got)
	if id == "" {
		return "", fmt.Errorf("path %q has no id", path)
	}
	return id, nil
}

// jsonPath walks a simple JSONPath: $, .key, and [index].
func jsonPath(v any, path string) (any, error) {
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("path %q must start with $", path)
	}
	cur := v
	for rest := path[1:]; rest != ""; {
		switch {
		case strings.HasPrefix(rest, "."):
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			key := rest[:end]
			rest = rest[end:]
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("path %q: %q is not an object", path, key)
			}
			cur, ok = m[key]
			if !ok {
				return nil, fmt.Errorf("path %q: no %q", path, key)
			}
		case strings.HasPrefix(rest, "["):
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q: unclosed [", path)
			}
			idx, err := strconv.Atoi(rest[1:end])
			if err != nil {
				return nil, fmt.Errorf("path %q: bad index", path)
			}
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("path %q: not an array", path)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("path %q: index %d out of range", path, idx)
			}
			cur = arr[idx]
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("path %q: unexpected %q", path, rest)
		}
	}
	return cur, nil
}

// collectTables is the distinct tables of the scenarios, in order.
func collectTables(scenarios []WritesScenario) []string {
	seen := map[string]bool{}
	var out []string
	for _, sc := range scenarios {
		for _, table := range sc.Tables {
			if !seen[table] {
				seen[table] = true
				out = append(out, table)
			}
		}
	}
	return out
}

// localHostRule is the refusal text both local checks share.
const localHostRule = "the writes runner runs only against localhost, 127.0.0.1, ::1, or a compose service name (a letter, then letters, digits, - or _, no dot)"

// composeName is a compose service name: no dot, so it never resolves through
// public DNS, and a leading letter, so a numeric or hex IPv4 shorthand such as
// 2130706433 or 0x7f000001 is never taken for a name.
var composeName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// checkLocalDB refuses a DSN that would connect anywhere but a local host. The
// DSN is parsed the way the driver parses it (a URL or the keyword form), and
// the final host, every fallback host and any hostaddr parameter are checked,
// so a ?host= or ?hostaddr= override and a multi-host list cannot slip past.
// The parse error is not wrapped: it may quote the DSN.
func checkLocalDB(dsn, name string) error {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("compare writes: %s does not parse as a Postgres connection string", name)
	}
	hosts := []string{cfg.Host}
	for _, f := range cfg.Fallbacks {
		hosts = append(hosts, f.Host)
	}
	if addr, ok := cfg.RuntimeParams["hostaddr"]; ok {
		hosts = append(hosts, addr)
	}
	for _, host := range hosts {
		if !isLocalHost(host) {
			return fmt.Errorf("compare writes: %s host %q is not local; %s", name, host, localHostRule)
		}
	}
	return nil
}

// checkLocalURL refuses a backend URL whose host is not local: the writes
// runner creates, pays and reverses rows, so it must never reach a deployed
// backend.
func checkLocalURL(raw, name string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("compare writes: --%s is not an http(s) URL", name)
	}
	if !isLocalHost(u.Hostname()) {
		return fmt.Errorf("compare writes: --%s host %q is not local; %s", name, u.Hostname(), localHostRule)
	}
	return nil
}

// isLocalHost reports whether host is local: localhost, the loopback
// literals 127.0.0.1 and ::1, or a compose service name. Every other IP
// literal is refused (a compose name has no dot or colon and starts with a
// letter, so no IPv4 or IPv6 literal matches it), and so is a socket path.
func isLocalHost(host string) bool {
	h := strings.ToLower(host)
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return composeName.MatchString(h)
}

// redactErr replaces the DSN in an error's text, so a connection error never
// carries a password.
func redactErr(err error, dsn string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if dsn != "" {
		msg = strings.ReplaceAll(msg, dsn, "<redacted>")
	}
	return errors.New(msg)
}

// ParseWrites reads a compare-writes.yaml document and validates it: every
// scenario has a unique id, every step has a method and a path, every
// placeholder in a path was extracted earlier, and every table is a real
// table that is not Go-only.
func ParseWrites(data []byte) (WritesScenarios, error) {
	var w WritesScenarios
	if err := yaml.Unmarshal(data, &w); err != nil {
		return WritesScenarios{}, fmt.Errorf("compare writes: %w", err)
	}
	if err := w.validate(); err != nil {
		return WritesScenarios{}, err
	}
	return w, nil
}

// LoadWritesFile reads the scenarios from path.
func LoadWritesFile(path string) (WritesScenarios, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller argument (the --scenarios flag)
	if err != nil {
		return WritesScenarios{}, err
	}
	return ParseWrites(data)
}

// validate refuses a scenario set that cannot run: no scenarios, a duplicate
// id, a step without a method or a path, a placeholder that no earlier step
// extracted, a placeholder extracted twice, a placeholder used after a DELETE
// on it, a Go-only table, or a table name that is not a plain identifier.
func (w WritesScenarios) validate() error {
	if len(w.Scenarios) == 0 {
		return errors.New("compare writes: no scenarios")
	}
	ids := make(map[string]bool, len(w.Scenarios))
	// The extracted names are shared across scenarios, because a scenario
	// can create an id the next scenario uses (master data, then invoices).
	extracted := map[string]bool{}
	deleted := map[string]string{}
	for i, sc := range w.Scenarios {
		where := fmt.Sprintf("scenario %d", i+1)
		if strings.TrimSpace(sc.ID) == "" {
			return fmt.Errorf("compare writes: %s has no id", where)
		}
		where = fmt.Sprintf("scenario %s", sc.ID)
		if ids[sc.ID] {
			return fmt.Errorf("compare writes: %s is declared twice", sc.ID)
		}
		ids[sc.ID] = true
		if len(sc.Steps) == 0 {
			return fmt.Errorf("compare writes: %s has no steps", where)
		}
		for _, table := range sc.Tables {
			if !tableName.MatchString(table) {
				return fmt.Errorf("compare writes: %s: table %q is not a valid table name", where, table)
			}
			if goOnlyTables[table] {
				return fmt.Errorf("compare writes: %s: table %q is Go-only and is not compared", where, table)
			}
		}
		for _, step := range sc.Steps {
			stepWhere := fmt.Sprintf("%s step %s", where, step.ID)
			if strings.TrimSpace(step.ID) == "" {
				return fmt.Errorf("compare writes: %s has no id", stepWhere)
			}
			if strings.TrimSpace(step.Method) == "" {
				return fmt.Errorf("compare writes: %s has no method", stepWhere)
			}
			if strings.TrimSpace(step.Path) == "" {
				return fmt.Errorf("compare writes: %s has no path", stepWhere)
			}
			if !strings.HasPrefix(step.Path, "/") {
				return fmt.Errorf("compare writes: %s: path %q has no leading slash", stepWhere, step.Path)
			}
			for _, name := range placeholders(step.Path) {
				if !extracted[name] {
					return fmt.Errorf("compare writes: %s: {%s} has no earlier extract", stepWhere, name)
				}
				if by, ok := deleted[name]; ok {
					return fmt.Errorf("compare writes: %s: {%s} was deleted by step %s; create a separate one to delete", stepWhere, name, by)
				}
			}
			for _, name := range placeholders(step.BodyString()) {
				if !extracted[name] {
					return fmt.Errorf("compare writes: %s: body {%s} has no earlier extract", stepWhere, name)
				}
				if by, ok := deleted[name]; ok {
					return fmt.Errorf("compare writes: %s: body {%s} was deleted by step %s; create a separate one to delete", stepWhere, name, by)
				}
			}
			// A DELETE deletes or deactivates the row its path names: no
			// later step may use that row again.
			if strings.EqualFold(step.Method, "DELETE") {
				for _, name := range placeholders(step.Path) {
					deleted[name] = step.ID
				}
			}
			for name, path := range step.Extract {
				// A placeholder name is a plain identifier; the path is the value.
				if strings.TrimSpace(name) == "" {
					return fmt.Errorf("compare writes: %s has an empty extract name", stepWhere)
				}
				if !strings.HasPrefix(path, "$") {
					return fmt.Errorf("compare writes: %s: extract %s path %q must start with $", stepWhere, name, path)
				}
				// A second extract under one name would silently point every
				// later step at a different row.
				if extracted[name] {
					return fmt.Errorf("compare writes: %s: {%s} is extracted twice; give each extract its own name", stepWhere, name)
				}
				extracted[name] = true
			}
		}
	}
	return nil
}

// BodyString is the step's body as a string, for placeholder validation.
func (s WriteStep) BodyString() string {
	if s.Body == nil {
		return ""
	}
	return fmt.Sprint(s.Body)
}
