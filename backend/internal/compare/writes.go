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
	"strconv"
	"strings"
	"time"

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
// the canonical row text, not a connection string.
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

// WritesStepResult is one request's status pair. Path is the step's template
// path, not the expanded one, so a report never carries a backend's id.
type WritesStepResult struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Python int    `json:"python_status"`
	Go     int    `json:"go_status"`
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
	// cookie. The runner never POSTs to Go.
	GoToken   string
	Scenarios WritesScenarios
	Client    *http.Client
	// Now is the run's clock, for the report's timestamp.
	Now func() time.Time
	// Env reads the process environment; nil means os.Getenv. It is used for
	// the RESEND_API_KEY guard and the WEBHOOK_HMAC_SECRET signature.
	Env func(string) string
}

// idPair is one placeholder's id on each backend.
type idPair struct {
	python string
	goID   string
}

// RunWrites runs the write scenarios against both backends, then compares the
// rows of the tables they touched. It refuses to run unless both database
// hosts are local and RESEND_API_KEY is unset: a local run must never send a
// mail or reach a remote database. A non-2xx answer is a hard failure: the
// run stops there, so later steps never run against a broken flow.
//
// The DSNs are never logged, never returned in an error and never written to
// the report. Each backend's id is mapped to the other's before the row
// comparison, so a row's Python id is compared as its Go id.
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
		return report, errors.New("compare writes: RESEND_API_KEY is set; refusing to send mail")
	}
	if err := checkLocalDB(o.PythonDB, "python-db"); err != nil {
		return report, err
	}
	if err := checkLocalDB(o.GoDB, "go-db"); err != nil {
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

	extracted := map[string]idPair{}
	idMap := map[string]string{}
	for _, sc := range o.Scenarios.Scenarios {
		result, err := runScenario(ctx, client, o, sc, extracted, idMap, session)
		report.Scenarios = append(report.Scenarios, result)
		if err != nil {
			return report, err
		}
	}

	diffs, err := Rows(ctx, pyDB, goDB, collectTables(o.Scenarios.Scenarios), idMap)
	if err != nil {
		return report, fmt.Errorf("compare writes: row comparison: %w", redactErr(redactErr(err, o.PythonDB), o.GoDB))
	}
	report.RowDiffs = diffs
	report.Differences = len(diffs)
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

// runScenario runs one scenario's steps in order. It returns the result so far
// and the first hard failure, so a report still shows the steps that ran.
func runScenario(ctx context.Context, client *http.Client, o WritesOptions, sc WritesScenario, extracted map[string]idPair, idMap map[string]string, session string) (WritesScenarioResult, error) {
	result := WritesScenarioResult{ID: sc.ID, Classify: sc.Classify}
	for _, step := range sc.Steps {
		pyPath, err := expandPath(step.Path, extracted, false)
		if err != nil {
			return result, fmt.Errorf("scenario %s: step %s: %w", sc.ID, step.ID, err)
		}
		goPath, err := expandPath(step.Path, extracted, true)
		if err != nil {
			return result, fmt.Errorf("scenario %s: step %s: %w", sc.ID, step.ID, err)
		}
		pyBody, err := buildBody(step.Body, extracted, false)
		if err != nil {
			return result, fmt.Errorf("scenario %s: step %s: %w", sc.ID, step.ID, err)
		}
		goBody, err := buildBody(step.Body, extracted, true)
		if err != nil {
			return result, fmt.Errorf("scenario %s: step %s: %w", sc.ID, step.ID, err)
		}

		pyResp, err := send(ctx, client, o, o.PythonURL, step.Method, pyPath, pyBody, session, step.Sign)
		if err != nil {
			return result, fmt.Errorf("scenario %s: step %s: python: %w", sc.ID, step.ID, redactErr(err, o.PythonURL))
		}
		goResp, err := send(ctx, client, o, o.GoURL, step.Method, goPath, goBody, o.GoToken, step.Sign)
		if err != nil {
			return result, fmt.Errorf("scenario %s: step %s: go: %w", sc.ID, step.ID, redactErr(err, o.GoURL))
		}
		result.Steps = append(result.Steps, WritesStepResult{
			ID: step.ID, Method: step.Method, Path: step.Path,
			Python: pyResp.Status, Go: goResp.Status,
		})
		if pyResp.Status < 200 || pyResp.Status >= 300 {
			return result, fmt.Errorf("scenario %s: step %s: python status %d", sc.ID, step.ID, pyResp.Status)
		}
		if goResp.Status < 200 || goResp.Status >= 300 {
			return result, fmt.Errorf("scenario %s: step %s: go status %d", sc.ID, step.ID, goResp.Status)
		}
		for name, path := range step.Extract {
			pyID, err := extractID(pyResp.Body, path)
			if err != nil {
				return result, fmt.Errorf("scenario %s: step %s: python %s: %w", sc.ID, step.ID, name, err)
			}
			goID, err := extractID(goResp.Body, path)
			if err != nil {
				return result, fmt.Errorf("scenario %s: step %s: go %s: %w", sc.ID, step.ID, name, err)
			}
			extracted[name] = idPair{python: pyID, goID: goID}
			idMap[pyID] = goID
		}
	}
	return result, nil
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

// checkLocalDB refuses a DSN whose host is not local. The local set is
// localhost, 127.0.0.1, ::1 and a compose service name without a dot.
func checkLocalDB(dsn, name string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("compare writes: %s is not a valid URL: %w", name, redactErr(err, dsn))
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("compare writes: %s has no host", name)
	}
	if isLocalDBHost(host) {
		return nil
	}
	return fmt.Errorf("compare writes: %s host %q is not local; the writes runner runs only against localhost, 127.0.0.1, ::1, or a compose service name without a dot", name, host)
}

// isLocalDBHost reports whether host is a local database host.
func isLocalDBHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return !strings.Contains(host, ".")
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
// extracted, a Go-only table, or a table name that is not a plain identifier.
func (w WritesScenarios) validate() error {
	if len(w.Scenarios) == 0 {
		return errors.New("compare writes: no scenarios")
	}
	ids := make(map[string]bool, len(w.Scenarios))
	// The extracted names are shared across scenarios, because a scenario
	// can create an id the next scenario uses (master data, then invoices).
	extracted := map[string]bool{}
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
			}
			for _, name := range placeholders(step.BodyString()) {
				if !extracted[name] {
					return fmt.Errorf("compare writes: %s: body {%s} has no earlier extract", stepWhere, name)
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
