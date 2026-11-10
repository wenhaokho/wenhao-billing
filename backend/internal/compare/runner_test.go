package compare

import (
	"context"
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
	if got := ExitCode(report, nil); got != 1 {
		t.Fatalf("exit code %d, want 1", got)
	}
}

// TestRunnerFoldsRedirectHosts checks decision 1: each backend's own host is
// set on the Allowed entries, so a redirect's Location compares equal.
func TestRunnerFoldsRedirectHosts(t *testing.T) {
	t.Parallel()
	redirect := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginHandler(w, r)
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
	if got := ExitCode(report, nil); got != 1 {
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
		if got := ExitCode(report, err); got != 0 {
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
		if got := ExitCode(report, err); got != 1 {
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
		if got := ExitCode(report, err); got != 2 {
			t.Fatalf("exit code %d, want 2", got)
		}
		if strings.Contains(err.Error(), "pw") {
			t.Fatalf("the login error names the password: %v", err)
		}
	})
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
	seen := map[string]bool{}
	for _, c := range corpus.Cases {
		if seen[c.ID] {
			t.Fatalf("case %s is declared twice", c.ID)
		}
		seen[c.ID] = true
	}
	for _, want := range []string{"oauth-authorization-server", "oauth-protected-resource", "fx-rates-latest", "payment-detail"} {
		if !seen[want] {
			t.Fatalf("the corpus has no case %s", want)
		}
	}
	// Decision 3: the balance sheet runs with as_of = today, so PN 97 does
	// not arise.
	for _, c := range corpus.Cases {
		if c.ID != "report-balance-sheet" {
			continue
		}
		if len(c.Queries) != 1 || c.Queries[0] != "as_of={today}" {
			t.Fatalf("the balance-sheet case runs %v, want as_of={today}", c.Queries)
		}
	}
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
