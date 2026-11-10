package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// call is one request a test backend saw.
type call struct {
	method, target, cookie string
}

// recorder is an httptest handler that records every request it answers, for
// the "never sends non-GET" guard.
type recorder struct {
	mu     sync.Mutex
	calls  []call
	handle http.HandlerFunc
}

func (rc *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc.mu.Lock()
	rc.calls = append(rc.calls, call{r.Method, r.URL.RequestURI(), r.Header.Get("Cookie")})
	rc.mu.Unlock()
	rc.handle(w, r)
}

// seen is a copy of the requests so far.
func (rc *recorder) seen() []call {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]call(nil), rc.calls...)
}

// start serves the recorder and closes it when the test ends.
func (rc *recorder) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(rc)
	t.Cleanup(srv.Close)
	return srv
}

// json writes a JSON body with a fixed status.
func jsonBody(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// loginHandler answers Python's POST /auth/login with a session cookie.
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login" {
		http.SetCookie(w, &http.Cookie{Name: platform.SessionCookie, Value: "py-session"})
		jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		return
	}
	http.NotFound(w, r)
}

// run is a helper: it runs a corpus against the two servers with a Python
// password (one login POST) and a Go token.
func run(t *testing.T, py, gov *httptest.Server, corpus Corpus, allowed Allowed) (Report, error) {
	t.Helper()
	return RunReads(context.Background(), ReadOptions{
		PythonURL: py.URL, GoURL: gov.URL,
		PythonEmail: "owner@example.test", PythonPassword: "pw",
		GoToken: "go-session", Corpus: corpus, Allowed: allowed,
		Client: py.Client(),
	})
}

func TestRunnerDiscoversIdsFromLists(t *testing.T) {
	t.Parallel()
	// Five customers, so first (1), last (5) and a middle (3) are distinct
	// from the two ids the selection leaves out.
	list := `[{"customer_id":1},{"customer_id":2},{"customer_id":3},{"customer_id":4},{"customer_id":5}]`
	detail := func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/customers/")
		jsonBody(w, http.StatusOK, fmt.Sprintf(`{"customer_id":%s}`, id))
	}
	py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			loginHandler(w, r)
		case r.URL.Path == "/api/v1/customers":
			jsonBody(w, http.StatusOK, list)
		default:
			detail(w, r)
		}
	}}
	gov := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/customers" {
			jsonBody(w, http.StatusOK, list)
			return
		}
		detail(w, r)
	}}
	pySrv, goSrv := py.start(t), gov.start(t)

	corpus := Corpus{Cases: []Case{
		{ID: "customers", Route: "GET /api/v1/customers", Discover: []IDSource{{Name: "customer_id"}}},
		{ID: "customer-detail", Route: "GET /api/v1/customers/{customer_id}"},
	}}
	report, err := run(t, pySrv, goSrv, corpus, Allowed{})
	if err != nil {
		t.Fatalf("RunReads: %v", err)
	}
	if report.Differences != 0 {
		t.Fatalf("report has %d differences, want 0: %+v", report.Differences, report.Cases)
	}

	got := map[string]bool{}
	for _, c := range gov.seen() {
		if strings.HasPrefix(c.target, "/api/v1/customers/") {
			got[strings.TrimPrefix(c.target, "/api/v1/customers/")] = true
		}
	}
	want := map[string]bool{"1": true, "5": true, "3": true}
	if len(got) != len(want) {
		t.Fatalf("detail ids %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("detail ids %v, want %v", got, want)
		}
	}
	for _, absent := range []string{"2", "4"} {
		if got[absent] {
			t.Fatalf("detail id %s was requested; only first, last and a middle are wanted", absent)
		}
	}
}

func TestRunnerReportsOnlyUnallowed(t *testing.T) {
	t.Parallel()
	py := &recorder{handle: loginHandler}
	py.handle = func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginHandler(w, r)
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/bills":
			jsonBody(w, http.StatusOK, `{"amount":"10.0000","count":10,"name":"python"}`)
		case "/api/v1/items":
			jsonBody(w, http.StatusOK, `{"total":"0.0000"}`)
		default:
			http.NotFound(w, r)
		}
	}
	gov := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/bills":
			jsonBody(w, http.StatusOK, `{"amount":"10.0000","count":10.0,"name":"go"}`)
		case "/api/v1/items":
			jsonBody(w, http.StatusOK, `{"total":"0"}`)
		default:
			http.NotFound(w, r)
		}
	}}
	pySrv, goSrv := py.start(t), gov.start(t)

	allowed, err := ParseAllowed([]byte("- route: GET /api/v1/bills\n  path: $.count\n  rule: number-equal\n  note: PN 104\n"))
	if err != nil {
		t.Fatalf("ParseAllowed: %v", err)
	}
	corpus := Corpus{Cases: []Case{
		{ID: "bills", Route: "GET /api/v1/bills"},
		{ID: "items", Route: "GET /api/v1/items"},
	}}
	report, err := run(t, pySrv, goSrv, corpus, allowed)
	if err != nil {
		t.Fatalf("RunReads: %v", err)
	}
	// The allowed count (10 vs 10.0), the equal amount and the equal total
	// are dropped; only the differing name is reported.
	if report.Differences != 1 {
		t.Fatalf("report has %d differences, want 1: %+v", report.Differences, report.Cases)
	}
	bills := findCase(t, report, "bills")
	if len(bills.Differences) != 1 || bills.Differences[0].Path != "$.name" {
		t.Fatalf("bills differences %+v, want only $.name", bills.Differences)
	}
	if got := ExitCode(report, nil, false); got != 1 {
		t.Fatalf("exit code %d, want 1", got)
	}
}

// TestRunnerFoldsRedirectHosts checks decision 1: each backend's own host is
// set on the Allowed entries, so a redirect's Location compares equal.
func TestRunnerFoldsRedirectHosts(t *testing.T) {
	t.Parallel()
	redirect := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginHandler(w, r)
			return
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
			return
		}
		w.Header().Set("Location", "http://"+r.Host+"/oauth/authorize?client_id=c&state=s")
		w.WriteHeader(http.StatusFound)
	}
	py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) { redirect(w, r) }}
	gov := &recorder{handle: func(w http.ResponseWriter, r *http.Request) { redirect(w, r) }}
	pySrv, goSrv := py.start(t), gov.start(t)

	corpus := Corpus{Cases: []Case{{ID: "authorize", Route: "GET /oauth/authorize"}}}
	report, err := run(t, pySrv, goSrv, corpus, Allowed{})
	if err != nil {
		t.Fatalf("RunReads: %v", err)
	}
	if report.Differences != 0 {
		t.Fatalf("report has %d differences, want 0 (the two hosts fold away): %+v", report.Differences, report.Cases)
	}
}

func TestRunnerClassifiesByCaseNotAllowed(t *testing.T) {
	t.Parallel()
	py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginHandler(w, r)
			return
		}
		jsonBody(w, http.StatusOK, `{"status":"PENDING_MANUAL_REVIEW"}`)
	}}
	gov := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"status":"CLEARED"}`)
	}}
	pySrv, goSrv := py.start(t), gov.start(t)

	// A difference under a case classified as PN 6 is reported, not hidden:
	// the classification only names the note in the report.
	corpus := Corpus{Cases: []Case{
		{ID: "pn6-identical-open-invoices", Route: "GET /api/v1/payments/awaiting-review", Classify: "PN 6"},
	}}
	report, err := run(t, pySrv, goSrv, corpus, Allowed{})
	if err != nil {
		t.Fatalf("RunReads: %v", err)
	}
	if report.Differences != 1 {
		t.Fatalf("report has %d differences, want 1", report.Differences)
	}
	c := findCase(t, report, "pn6-identical-open-invoices")
	if c.Classify != "PN 6" || len(c.Differences) != 1 {
		t.Fatalf("case %+v, want a PN 6 classification and its difference", c)
	}
	if got := ExitCode(report, nil, false); got != 1 {
		t.Fatalf("exit code %d, want 1", got)
	}
}

func TestRunnerNeverSendsNonGet(t *testing.T) {
	t.Parallel()
	newBackend := func() *recorder {
		return &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/auth/login" {
				loginHandler(w, r)
				return
			}
			jsonBody(w, http.StatusOK, `{"ok":true}`)
		}}
	}
	corpus := Corpus{Cases: []Case{
		{ID: "me", Route: "GET /api/v1/auth/me"},
		{ID: "users", Route: "GET /api/v1/users"},
	}}
	t.Run("a password sends one Python POST", func(t *testing.T) {
		t.Parallel()
		py, gov := newBackend(), newBackend()
		pySrv, goSrv := py.start(t), gov.start(t)
		if _, err := run(t, pySrv, goSrv, corpus, Allowed{}); err != nil {
			t.Fatalf("RunReads: %v", err)
		}
		assertOnlyPythonLoginPost(t, py.seen(), gov.seen())
	})
	t.Run("a cookie sends no POST at all", func(t *testing.T) {
		t.Parallel()
		py, gov := newBackend(), newBackend()
		pySrv, goSrv := py.start(t), gov.start(t)
		_, err := RunReads(context.Background(), ReadOptions{
			PythonURL: pySrv.URL, GoURL: goSrv.URL,
			PythonEmail: "owner@example.test", PythonCookie: "owner-cookie",
			GoToken: "go-session", Corpus: corpus, Allowed: Allowed{}, Client: pySrv.Client(),
		})
		if err != nil {
			t.Fatalf("RunReads: %v", err)
		}
		for _, c := range py.seen() {
			if c.method != http.MethodGet {
				t.Fatalf("Python saw %s %s; a cookie must send no POST", c.method, c.target)
			}
		}
		for _, c := range gov.seen() {
			if c.method != http.MethodGet {
				t.Fatalf("Go saw %s %s; the runner never POSTs to Go", c.method, c.target)
			}
		}
	})
}

// assertOnlyPythonLoginPost fails unless Go saw GETs only and Python saw
// exactly one non-GET, its POST /auth/login.
func assertOnlyPythonLoginPost(t *testing.T, py, gov []call) {
	t.Helper()
	posts := 0
	for _, c := range py {
		if c.method == http.MethodPost {
			posts++
			if c.target != "/api/v1/auth/login" {
				t.Fatalf("Python saw POST %s, want only /api/v1/auth/login", c.target)
			}
		}
	}
	if posts != 1 {
		t.Fatalf("Python saw %d POSTs, want exactly 1 (the login)", posts)
	}
	for _, c := range gov {
		if c.method != http.MethodGet {
			t.Fatalf("Go saw %s %s; the runner never POSTs to Go", c.method, c.target)
		}
		if c.cookie != platform.SessionCookie+"=go-session" {
			t.Fatalf("Go request %s carried cookie %q, want the token", c.target, c.cookie)
		}
	}
}

func TestRunnerExitCodes(t *testing.T) {
	t.Parallel()
	corpus := Corpus{Cases: []Case{{ID: "me", Route: "GET /api/v1/auth/me"}}}
	t.Run("clean is 0", func(t *testing.T) {
		t.Parallel()
		py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/auth/login" {
				loginHandler(w, r)
				return
			}
			jsonBody(w, http.StatusOK, `{"same":true}`)
		}}
		gov := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
			jsonBody(w, http.StatusOK, `{"same":true}`)
		}}
		pySrv, goSrv := py.start(t), gov.start(t)
		report, err := run(t, pySrv, goSrv, corpus, Allowed{})
		if got := ExitCode(report, err, false); got != 0 {
			t.Fatalf("exit code %d, want 0: %v", got, err)
		}
	})
	t.Run("a difference is 1", func(t *testing.T) {
		t.Parallel()
		py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/auth/login" {
				loginHandler(w, r)
				return
			}
			jsonBody(w, http.StatusOK, `{"same":true}`)
		}}
		gov := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
			jsonBody(w, http.StatusOK, `{"same":false}`)
		}}
		pySrv, goSrv := py.start(t), gov.start(t)
		report, err := run(t, pySrv, goSrv, corpus, Allowed{})
		if got := ExitCode(report, err, false); got != 1 {
			t.Fatalf("exit code %d, want 1: %v", got, err)
		}
	})
	t.Run("an error is 2", func(t *testing.T) {
		t.Parallel()
		py := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
			jsonBody(w, http.StatusUnauthorized, `{"detail":"invalid credentials"}`)
		}}
		gov := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
			jsonBody(w, http.StatusOK, `{"same":true}`)
		}}
		pySrv, goSrv := py.start(t), gov.start(t)
		report, err := run(t, pySrv, goSrv, corpus, Allowed{})
		if err == nil {
			t.Fatal("RunReads succeeded with a refused login, want an error")
		}
		if got := ExitCode(report, err, false); got != 2 {
			t.Fatalf("exit code %d, want 2", got)
		}
		if strings.Contains(err.Error(), "pw") {
			t.Fatalf("the login error names the password: %v", err)
		}
	})
	t.Run("a skip is 1, or 0 with allow-skips", func(t *testing.T) {
		t.Parallel()
		// A skip with no difference, so ExitCode alone decides.
		report := Report{Cases: []CaseResult{{ID: "detail", Skipped: "no {customer_id}: list empty or failed"}}, Summary: Summary{Skipped: 1}}
		if got := ExitCode(report, nil, false); got != 1 {
			t.Fatalf("exit code %d, want 1 for a skip", got)
		}
		if got := ExitCode(report, nil, true); got != 0 {
			t.Fatalf("exit code %d, want 0 for a skip with allow-skips", got)
		}
	})
}

// TestRunnerSignInCheck pins the sign-in guard: a backend that does not answer
// /auth/me with 200 fails the whole run, so a run where everything 401s can
// never look clean. The response body is never printed.
func TestRunnerSignInCheck(t *testing.T) {
	t.Parallel()
	corpus := Corpus{Cases: []Case{{ID: "me", Route: "GET /api/v1/auth/me"}}}
	t.Run("Python 401 is exit 2", func(t *testing.T) {
		t.Parallel()
		py := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
			jsonBody(w, http.StatusUnauthorized, `{"detail":"python secret body"}`)
		}}
		gov := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		}}
		pySrv, goSrv := py.start(t), gov.start(t)
		report, err := RunReads(context.Background(), ReadOptions{
			PythonURL: pySrv.URL, GoURL: goSrv.URL,
			PythonEmail: "owner@example.test", PythonCookie: "owner-cookie",
			GoToken: "go-session", Corpus: corpus, Allowed: Allowed{}, Client: pySrv.Client(),
		})
		assertSignInError(t, report, err, "python", "python secret body")
	})
	t.Run("Go 401 is exit 2", func(t *testing.T) {
		t.Parallel()
		py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/auth/login" {
				loginHandler(w, r)
				return
			}
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		}}
		gov := &recorder{handle: func(w http.ResponseWriter, _ *http.Request) {
			jsonBody(w, http.StatusUnauthorized, `{"detail":"go secret body"}`)
		}}
		pySrv, goSrv := py.start(t), gov.start(t)
		report, err := run(t, pySrv, goSrv, corpus, Allowed{})
		assertSignInError(t, report, err, "go", "go secret body")
	})
}

// assertSignInError fails unless err is the sign-in check failure for backend
// on 401, without the body, and the run exits 2.
func assertSignInError(t *testing.T, report Report, err error, backend, body string) {
	t.Helper()
	if err == nil {
		t.Fatal("RunReads succeeded, want a sign-in check error")
	}
	want := "sign-in check failed on " + backend + ": 401"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want it to contain %q", err, want)
	}
	if strings.Contains(err.Error(), body) {
		t.Fatalf("the sign-in error leaks the body: %v", err)
	}
	if got := ExitCode(report, err, false); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
}

// TestRunnerSkipsEmptyLists pins the skip guard: a detail case whose id list is
// empty (or failed) is recorded as skipped and is not clean, unless
// --allow-skips is given.
func TestRunnerSkipsEmptyLists(t *testing.T) {
	t.Parallel()
	corpus := Corpus{Cases: []Case{
		{ID: "customers", Route: "GET /api/v1/customers", Discover: []IDSource{{Name: "customer_id"}}},
		{ID: "customer-detail", Route: "GET /api/v1/customers/{customer_id}"},
	}}
	backend := func(listStatus int, list string) *recorder {
		return &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/auth/login":
				loginHandler(w, r)
			case "/api/v1/auth/me":
				jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
			case "/api/v1/customers":
				jsonBody(w, listStatus, list)
			default:
				jsonBody(w, http.StatusOK, `{"ok":true}`)
			}
		}}
	}
	t.Run("an empty list is skipped and is not clean", func(t *testing.T) {
		t.Parallel()
		py, gov := backend(http.StatusOK, `[]`), backend(http.StatusOK, `[]`)
		pySrv, goSrv := py.start(t), gov.start(t)
		report, err := run(t, pySrv, goSrv, corpus, Allowed{})
		if err != nil {
			t.Fatalf("RunReads: %v", err)
		}
		detail := findCase(t, report, "customer-detail")
		if want := "no {customer_id}: list empty or failed"; detail.Skipped != want {
			t.Fatalf("skipped %q, want %q", detail.Skipped, want)
		}
		if report.Summary.Skipped != 1 {
			t.Fatalf("summary skipped %d, want 1: %+v", report.Summary.Skipped, report.Cases)
		}
		if got := ExitCode(report, nil, false); got != 1 {
			t.Fatalf("exit code %d, want 1 with a skip", got)
		}
		if got := ExitCode(report, nil, true); got != 0 {
			t.Fatalf("exit code %d, want 0 with --allow-skips", got)
		}
	})
	t.Run("a failed list is skipped", func(t *testing.T) {
		t.Parallel()
		py, gov := backend(http.StatusUnauthorized, `{"detail":"no"}`), backend(http.StatusUnauthorized, `{"detail":"no"}`)
		pySrv, goSrv := py.start(t), gov.start(t)
		report, err := run(t, pySrv, goSrv, corpus, Allowed{})
		if err != nil {
			t.Fatalf("RunReads: %v", err)
		}
		if detail := findCase(t, report, "customer-detail"); detail.Skipped == "" {
			t.Fatalf("the detail case was not skipped: %+v", detail)
		}
		if report.Summary.Skipped != 1 {
			t.Fatalf("summary skipped %d, want 1", report.Summary.Skipped)
		}
	})
}

// TestRunnerEscapesDiscoveredIDs pins that a discovered id is path-escaped, so
// an id with a slash cannot change the request's route.
func TestRunnerEscapesDiscoveredIDs(t *testing.T) {
	t.Parallel()
	list := `[{"customer_id":"a/b"}]`
	detail := func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	}
	py := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginHandler(w, r)
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/customers":
			jsonBody(w, http.StatusOK, list)
		default:
			detail(w, r)
		}
	}}
	gov := &recorder{handle: func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/me":
			jsonBody(w, http.StatusOK, `{"user_id":"u"}`)
		case "/api/v1/customers":
			jsonBody(w, http.StatusOK, list)
		default:
			detail(w, r)
		}
	}}
	pySrv, goSrv := py.start(t), gov.start(t)
	corpus := Corpus{Cases: []Case{
		{ID: "customers", Route: "GET /api/v1/customers", Discover: []IDSource{{Name: "customer_id"}}},
		{ID: "customer-detail", Route: "GET /api/v1/customers/{customer_id}"},
	}}
	if _, err := run(t, pySrv, goSrv, corpus, Allowed{}); err != nil {
		t.Fatalf("RunReads: %v", err)
	}
	for _, c := range gov.seen() {
		if c.target == "/api/v1/customers/a%2Fb" {
			return
		}
	}
	t.Fatalf("Go never saw the escaped detail path; saw %+v", gov.seen())
}

// TestRedactURLChecksTheReportEveryRun: the report folds a userinfo password
// away, so it never carries a credential from the --python/--go URL.
func TestRedactURL(t *testing.T) {
	t.Parallel()
	got := redactURL("http://owner:s3cret@127.0.0.1:8000/api")
	if strings.Contains(got, "s3cret") {
		t.Fatalf("redactURL kept the password: %q", got)
	}
	if !strings.Contains(got, "owner") {
		t.Fatalf("redactURL dropped the user: %q", got)
	}
}

// TestReadClientPinsPolicy pins decision 1 and the proxy guard: the client
// never follows a redirect, even when a caller passes its own client, and the
// default client's transport has no proxy.
func TestReadClientPinsPolicy(t *testing.T) {
	t.Parallel()
	def := readClient(ReadOptions{})
	if err := def.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("the default client's CheckRedirect returned %v, want ErrUseLastResponse", err)
	}
	transport, ok := def.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the default client's transport is %T, want *http.Transport", def.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("the default client's transport has a proxy; a credential could be sent through it")
	}
	caller := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	got := readClient(ReadOptions{Client: caller})
	if got == caller {
		t.Fatal("readClient returned the caller's own client, not a copy")
	}
	if err := got.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("the caller's client kept its CheckRedirect: returned %v", err)
	}
	if err := caller.CheckRedirect(nil, nil); err != nil {
		t.Fatalf("readClient mutated the caller's client: CheckRedirect returned %v", err)
	}
}

// TestDifferenceJSONTags pins the report's snake_case difference keys.
func TestDifferenceJSONTags(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(Difference{Path: "$.a", Python: "1", Go: "2", Rule: RuleNumberEqual})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"path", "python", "go", "rule"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("Difference JSON %s has no %q key", data, key)
		}
	}
}

// TestCorpusFileLoads reads the real corpus, so a typo in it fails here.
func TestCorpusFileLoads(t *testing.T) {
	t.Parallel()
	corpus, err := LoadCorpusFile("../../port/compare-corpus.yaml")
	if err != nil {
		t.Fatalf("LoadCorpusFile: %v", err)
	}
	if len(corpus.Cases) < 40 {
		t.Fatalf("the corpus has %d cases, want the full read set", len(corpus.Cases))
	}
	byID := map[string]Case{}
	for _, c := range corpus.Cases {
		if _, dup := byID[c.ID]; dup {
			t.Fatalf("case %s is declared twice", c.ID)
		}
		byID[c.ID] = c
	}
	for _, want := range []string{"oauth-authorization-server", "oauth-protected-resource", "oauth-authorize-error", "fx-rates-latest", "payment-detail"} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("the corpus has no case %s", want)
		}
	}
	// Decision 3: the balance sheet runs with as_of = today, so PN 97 does
	// not arise.
	if got := byID["report-balance-sheet"].Queries; len(got) != 1 || got[0] != "as_of={today}" {
		t.Fatalf("the balance-sheet case runs %v, want as_of={today}", got)
	}
	// The frontend's own query variants, pinned so a change to a view is a
	// failure here.
	for id, want := range map[string][]string{
		"bills":              {"status=OPEN&status=PARTIAL", "status=DRAFT"},
		"projects":           {"status=ACTIVE", "status=ON_HOLD"},
		"quotations":         {"status=DRAFT&status=SENT", "customer_id={customer_id}", "project_id={project_id}"},
		"invoices":           {"status=DRAFT", "status=OPEN&status=SENT&status=PARTIAL"},
		"customers":          {"active=true"},
		"vendors":            {"active=true"},
		"fx-rates-latest":    {"from_currency=USD", "from_currency=SGD", "from_currency=USD&on_date={today}"},
		"payments-audit-log": {"action=CLEARED", "payer=compare-no-match", "from_date={today}&to_date={today}"},
	} {
		cs, ok := byID[id]
		if !ok {
			t.Fatalf("the corpus has no case %s", id)
		}
		for _, q := range want {
			if !contains(cs.Queries, q) {
				t.Fatalf("case %s runs %v, want it to include %q", id, cs.Queries, q)
			}
		}
	}
	// The quotations case must come after projects so {project_id} is known.
	if indexOfID(corpus, "quotations") < indexOfID(corpus, "projects") {
		t.Fatal("the quotations case runs before the projects case, so {project_id} is not discovered yet")
	}
	// payment_id has three list sources, so a payment detail survives an empty
	// awaiting-review.
	for _, id := range []string{"payments-audit-log", "payments-awaiting-review", "invoice-payments"} {
		if !discovers(byID[id], "payment_id") {
			t.Fatalf("case %s does not discover payment_id", id)
		}
	}
}

// contains reports whether a string slice holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// indexOfID is the corpus position of a case id, or -1.
func indexOfID(c Corpus, id string) int {
	for i, cs := range c.Cases {
		if cs.ID == id {
			return i
		}
	}
	return -1
}

// discovers reports whether a case names an id source.
func discovers(cs Case, name string) bool {
	for _, d := range cs.Discover {
		if d.Name == name {
			return true
		}
	}
	return false
}

// TestCorpusRefusesNonGet is the read-only guard: a POST case never validates.
func TestCorpusRefusesNonGet(t *testing.T) {
	t.Parallel()
	_, err := ParseCorpus([]byte("cases:\n  - id: write\n    route: POST /api/v1/invoices\n"))
	if err == nil || !strings.Contains(err.Error(), "GET only") {
		t.Fatalf("ParseCorpus error %v, want a GET-only refusal", err)
	}
}

// findCase is the report's case with id.
func findCase(t *testing.T, report Report, id string) CaseResult {
	t.Helper()
	for _, c := range report.Cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("report has no case %s", id)
	return CaseResult{}
}
