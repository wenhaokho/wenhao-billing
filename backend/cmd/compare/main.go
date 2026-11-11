// Command compare replays the frontend's calls against two backends and diffs
// their JSON, so the Go port can be checked side by side with the frozen
// Python backend before the switch-over.
//
//	compare reads --python URL --go URL --python-email E \
//	  [--python-password-stdin | --python-cookie-stdin] --go-token-stdin \
//	  --corpus FILE --allowed FILE [--allowed FILE ...] [--python-tz ZONE] \
//	  [--allow-skips] [--ledger-matches-aging] --out report.json
//
//	compare writes --python URL --python-db DSN --go URL --go-db DSN \
//	  --python-email E [--python-password-stdin | --python-cookie-stdin] \
//	  --go-token-stdin --scenarios FILE [--allowed FILE ...] --out report.json
//
// --allowed may be repeated; the files' entries are merged (the shipped
// allowed-diffs.yaml, plus an overlay such as allowed-diffs-prodcopy.yaml for
// the production-copy run only). Every entry whose route the run requested but
// that never applied to an answer is printed to stderr and listed under
// unused_allowed in the report: a warning, never a failure. A later file's
// entry that repeats, conflicts with or shadows an earlier file's is warned of
// on stderr when the files are loaded.
//
// A read case classified PN 166 (the ledger reports) also has its Go answer
// checked against itself; a failed check is a difference. With
// --ledger-matches-aging, Go's trial balance 1100/2000 must also equal Go's
// AR/AP aging totals: pass it only where Go's ledger holds every document (the
// production copy after `api backpost-ledger --apply`).
//
// stdin carries the credentials in a fixed order, one line each: the FIRST line
// is the Python credential (the password with --python-password-stdin, or the
// billing_session cookie with --python-cookie-stdin), the SECOND line is the Go
// token. Every corpus request is a GET. The only non-GET is Python's POST
// /auth/login, and only with --python-password-stdin; --python-cookie-stdin
// sends no POST at all. The Go token is sent as the billing_session cookie and
// the runner never POSTs to Go. The credentials must be piped in, never typed
// into a terminal.
//
// Every run also asks both backends GET /api/v1/auth/me and refuses to compare
// unless both answer 200, so a fully-failing run can never look clean.
//
// Exit codes: 0 clean, 1 differences or skips, 2 error.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // a named --python-tz works in the distroless image

	"github.com/wenhaokho/wenhao-billing/backend/internal/compare"
)

// commandIO is the process's three streams. They are injectable so a test can
// drive the command and capture (or search) everything it writes.
type commandIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func main() {
	if len(os.Args) < 2 {
		slog.Error("usage: compare reads --python URL --go URL --python-email E [--python-password-stdin | --python-cookie-stdin] --go-token-stdin --corpus FILE --allowed FILE [--allowed FILE ...] [--python-tz ZONE] [--allow-skips] [--ledger-matches-aging] --out report.json")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "reads":
		os.Exit(readsCommand(os.Args[2:], commandIO{os.Stdin, os.Stdout, os.Stderr}))
	case "writes":
		os.Exit(writesCommand(os.Args[2:], commandIO{os.Stdin, os.Stdout, os.Stderr}))
	default:
		slog.Error("compare: unknown subcommand", "subcommand", os.Args[1])
		os.Exit(2)
	}
}

// readsCommand runs `compare reads`. stdin carries the credentials in a fixed
// order, one line each: first the Python credential (the password or the
// cookie), then the Go token. None is ever logged, printed or written to the
// report; the credentials must be piped in, never typed at a terminal.
func readsCommand(args []string, cio commandIO) int {
	logger := slog.New(slog.NewTextHandler(cio.stderr, nil))
	fs := flag.NewFlagSet("reads", flag.ContinueOnError)
	fs.SetOutput(cio.stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: compare reads --python URL --go URL --python-email E [--python-password-stdin | --python-cookie-stdin] --go-token-stdin --corpus FILE --allowed FILE [--allowed FILE ...] [--python-tz ZONE] [--allow-skips] [--ledger-matches-aging] --out report.json\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "stdin carries the credentials in order, one line each: first the Python credential (the password or the cookie), then the Go token. Pipe them in; never type them at a terminal.")
		fs.PrintDefaults()
	}
	var (
		pythonURL      = fs.String("python", "", "Python base URL")
		goURL          = fs.String("go", "", "Go base URL")
		pythonEmail    = fs.String("python-email", "", "Python admin email")
		pythonPassword = fs.Bool("python-password-stdin", false, "read the Python password from the FIRST stdin line; sends one POST")
		pythonCookie   = fs.Bool("python-cookie-stdin", false, "read the Python billing_session cookie from the FIRST stdin line; sends no POST")
		goToken        = fs.Bool("go-token-stdin", false, "read the Go session token from the SECOND stdin line")
		corpusFile     = fs.String("corpus", "", "the compare-corpus.yaml path")
		allowedFiles   fileList
		pythonTZ       = fs.String("python-tz", "", "zone a naive Python timestamp is read in (default UTC)")
		allowSkips     = fs.Bool("allow-skips", false, "exit 0 when the only issue is skipped cases (an empty or failed list)")
		ledgerAging    = fs.Bool("ledger-matches-aging", false, "also check Go's trial balance 1100/2000 against Go's AR/AP aging (production copy after the back-post only)")
		outFile        = fs.String("out", "", "the report JSON path")
	)
	fs.Var(&allowedFiles, "allowed", "an allowed-diffs.yaml path; repeat it to merge several files")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := checkReadFlags(*pythonURL, *goURL, *pythonEmail, *pythonPassword, *pythonCookie, *goToken, *corpusFile, len(allowedFiles), *outFile); err != nil {
		logger.Error("compare reads: " + err.Error())
		return 2
	}
	if isTerminal(cio.stdin) {
		logger.Error("compare reads: pipe the secrets on stdin; do not type them into a terminal")
		return 2
	}

	reader := bufio.NewReader(cio.stdin)
	var password, cookie, token string
	var err error
	if *pythonPassword {
		if password, err = readLine(reader); err != nil {
			logger.Error("compare reads: reading the Python password", "err", err)
			return 2
		}
	}
	if *pythonCookie {
		if cookie, err = readLine(reader); err != nil {
			logger.Error("compare reads: reading the Python cookie", "err", err)
			return 2
		}
	}
	if token, err = readLine(reader); err != nil {
		logger.Error("compare reads: reading the Go token", "err", err)
		return 2
	}

	corpus, err := compare.LoadCorpusFile(*corpusFile)
	if err != nil {
		logger.Error("compare reads: loading the corpus", "err", err)
		return 2
	}
	allowed, overlaps, err := compare.LoadAllowedFiles(allowedFiles...)
	if err != nil {
		logger.Error("compare reads: loading the allowed differences", "err", err)
		return 2
	}
	warnOverlaps(logger, "compare reads", overlaps)
	if err := checkProductionCopy(allowedFiles, *ledgerAging); err != nil {
		logger.Error("compare reads: " + err.Error())
		return 2
	}
	tz, err := loadZone(*pythonTZ)
	if err != nil {
		logger.Error("compare reads: --python-tz", "err", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	report, runErr := compare.RunReads(ctx, compare.ReadOptions{
		PythonURL: *pythonURL, GoURL: *goURL, PythonEmail: *pythonEmail,
		PythonPassword: password, PythonCookie: cookie, GoToken: token,
		Corpus: corpus, Allowed: allowed, PythonTZ: tz, LedgerMatchesAging: *ledgerAging,
	})
	code := compare.ExitCode(report, runErr, *allowSkips)
	if err := writeReport(*outFile, report); err != nil {
		logger.Error("compare reads: writing the report", "err", err)
		return 2
	}
	if runErr != nil {
		logger.Error("compare reads: the run failed", "err", runErr, "report", *outFile)
		return code
	}
	warnUnused(logger, "compare reads", report.UnusedAllowed)
	_, _ = fmt.Fprintf(cio.stdout, "compare reads: cases=%d differences=%d skipped=%d report=%s\n",
		len(report.Cases), report.Differences, report.Summary.Skipped, *outFile)
	return code
}

// writesCommand runs `compare writes`. stdin carries the credentials in the
// same fixed order as reads: first the Python credential (the password or the
// cookie), then the Go token. The two DSNs are never printed, logged or written
// to the report. The run refuses to contact a remote database and refuses to
// run when its own environment has RESEND_API_KEY.
func writesCommand(args []string, cio commandIO) int {
	logger := slog.New(slog.NewTextHandler(cio.stderr, nil))
	fs := flag.NewFlagSet("writes", flag.ContinueOnError)
	fs.SetOutput(cio.stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "usage: compare writes --python URL --python-db DSN --go URL --go-db DSN --python-email E [--python-password-stdin | --python-cookie-stdin] --go-token-stdin --scenarios FILE [--allowed FILE ...] --out report.json\n\n")
		_, _ = fmt.Fprintln(fs.Output(), "Both databases and both backend URLs must be local: localhost, 127.0.0.1, ::1, or a compose service name (a letter, then letters, digits, - or _, no dot). A DSN may be a URL or the keyword form (host=... dbname=...); either is accepted only when it parses to a local host, with every fallback host and any hostaddr local too. Restore both databases from one dump before the run. Both backends must run with RESEND_API_KEY and CLOUDFLARE_API_TOKEN unset; the runner can check only its own environment. The webhook body is signed with WEBHOOK_HMAC_SECRET. stdin carries the credentials in order, one line each: first the Python credential, then the Go token. Pipe them in; never type them at a terminal.")
		fs.PrintDefaults()
	}
	var (
		pythonURL      = fs.String("python", "", "Python base URL")
		pythonDB       = fs.String("python-db", "", "Python database DSN (local only)")
		goURL          = fs.String("go", "", "Go base URL")
		goDB           = fs.String("go-db", "", "Go database DSN (local only)")
		pythonEmail    = fs.String("python-email", "", "Python admin email")
		pythonPassword = fs.Bool("python-password-stdin", false, "read the Python password from the FIRST stdin line; sends one POST")
		pythonCookie   = fs.Bool("python-cookie-stdin", false, "read the Python billing_session cookie from the FIRST stdin line; sends no POST")
		goToken        = fs.Bool("go-token-stdin", false, "read the Go session token from the SECOND stdin line")
		scenariosFile  = fs.String("scenarios", "", "the compare-writes.yaml path")
		allowedFiles   fileList
		outFile        = fs.String("out", "", "the report JSON path")
	)
	fs.Var(&allowedFiles, "allowed", "optional allowed-diffs.yaml the step answers are compared under; repeat it to merge several files")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := checkWriteFlags(*pythonURL, *pythonDB, *goURL, *goDB, *pythonEmail, *scenariosFile, *outFile, *pythonPassword, *pythonCookie, *goToken); err != nil {
		logger.Error("compare writes: " + err.Error())
		return 2
	}
	if isTerminal(cio.stdin) {
		logger.Error("compare writes: pipe the secrets on stdin; do not type them into a terminal")
		return 2
	}

	reader := bufio.NewReader(cio.stdin)
	var password, cookie, token string
	var err error
	if *pythonPassword {
		if password, err = readLine(reader); err != nil {
			logger.Error("compare writes: reading the Python password", "err", err)
			return 2
		}
	}
	if *pythonCookie {
		if cookie, err = readLine(reader); err != nil {
			logger.Error("compare writes: reading the Python cookie", "err", err)
			return 2
		}
	}
	if token, err = readLine(reader); err != nil {
		logger.Error("compare writes: reading the Go token", "err", err)
		return 2
	}

	scenarios, err := compare.LoadWritesFile(*scenariosFile)
	if err != nil {
		logger.Error("compare writes: loading the scenarios", "err", err)
		return 2
	}
	var allowed compare.Allowed
	if len(allowedFiles) > 0 {
		var overlaps []string
		if allowed, overlaps, err = compare.LoadAllowedFiles(allowedFiles...); err != nil {
			logger.Error("compare writes: loading the allowed differences", "err", err)
			return 2
		}
		warnOverlaps(logger, "compare writes", overlaps)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	report, runErr := compare.RunWrites(ctx, compare.WritesOptions{
		PythonURL: *pythonURL, GoURL: *goURL, PythonDB: *pythonDB, GoDB: *goDB,
		PythonEmail: *pythonEmail, PythonPassword: password, PythonCookie: cookie,
		GoToken: token, Scenarios: scenarios, Allowed: allowed,
	})
	code := compare.WritesExitCode(report, runErr)
	if err := writeReport(*outFile, report); err != nil {
		logger.Error("compare writes: writing the report", "err", err)
		return 2
	}
	if runErr != nil {
		logger.Error("compare writes: the run failed", "err", runErr, "report", *outFile)
		return code
	}
	warnUnused(logger, "compare writes", report.UnusedAllowed)
	_, _ = fmt.Fprintf(cio.stdout, "compare writes: scenarios=%d, %s, report=%s\n",
		len(report.Scenarios), compare.WritesSummary(report), *outFile)
	return code
}

// checkWriteFlags refuses a run with a missing or contradictory flag.
func checkWriteFlags(pythonURL, pythonDB, goURL, goDB, email, scenarios, out string, password, cookie, token bool) error {
	switch {
	case pythonURL == "":
		return errors.New("--python is required")
	case pythonDB == "":
		return errors.New("--python-db is required")
	case goURL == "":
		return errors.New("--go is required")
	case goDB == "":
		return errors.New("--go-db is required")
	case email == "":
		return errors.New("--python-email is required")
	case scenarios == "":
		return errors.New("--scenarios is required")
	case out == "":
		return errors.New("--out is required")
	case password && cookie:
		return errors.New("--python-password-stdin and --python-cookie-stdin are mutually exclusive")
	case !password && !cookie:
		return errors.New("one of --python-password-stdin or --python-cookie-stdin is required")
	case !token:
		return errors.New("--go-token-stdin is required")
	}
	return nil
}

// isTerminal reports whether r is a terminal: an *os.File whose mode has
// os.ModeCharDevice. The secrets must arrive on a pipe, so a run whose stdin is
// a terminal is refused before any secret is read.
func isTerminal(r io.Reader) bool {
	f, ok := r.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// checkReadFlags refuses a run with a missing or contradictory flag.
func checkReadFlags(pythonURL, goURL, email string, password, cookie, token bool, corpus string, allowed int, out string) error {
	switch {
	case pythonURL == "":
		return errors.New("--python is required")
	case goURL == "":
		return errors.New("--go is required")
	case email == "":
		return errors.New("--python-email is required")
	case corpus == "":
		return errors.New("--corpus is required")
	case allowed == 0:
		return errors.New("--allowed is required")
	case out == "":
		return errors.New("--out is required")
	case password && cookie:
		return errors.New("--python-password-stdin and --python-cookie-stdin are mutually exclusive")
	case !password && !cookie:
		return errors.New("one of --python-password-stdin or --python-cookie-stdin is required")
	case !token:
		return errors.New("--go-token-stdin is required")
	}
	return nil
}

// fileList is a repeatable path flag (--allowed a.yaml --allowed b.yaml).
type fileList []string

func (f *fileList) String() string { return strings.Join(*f, ",") }

// Set adds one path; an empty one is refused.
func (f *fileList) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("empty path")
	}
	*f = append(*f, v)
	return nil
}

// warnUnused prints one stderr warning per allowed entry the run never needed.
// An entry holds a route pattern, a JSONPath, a rule and a note, never data
// from an answer.
func warnUnused(logger *slog.Logger, cmd string, unused []compare.UnusedAllowed) {
	for _, u := range unused {
		logger.Warn(cmd+": allowed entry never matched in this run", "route", u.Route, "path", u.Path, "rule", u.Rule, "note", u.Note)
	}
}

// checkProductionCopy refuses an allowed file marked for the production-copy
// run (it ignores Go's ledger figures) unless --ledger-matches-aging is given,
// so that run always checks those figures against the agings instead.
func checkProductionCopy(paths []string, ledgerAging bool) error {
	if ledgerAging {
		return nil
	}
	for _, p := range paths {
		marked, err := compare.IsProductionCopyFile(p)
		if err != nil {
			return err
		}
		if marked {
			return fmt.Errorf("%s is for the production-copy run only and needs --ledger-matches-aging", p)
		}
	}
	return nil
}

// warnOverlaps prints one stderr warning per later-file allowed entry that
// repeats, conflicts with or shadows an earlier file's (LoadAllowedFiles).
func warnOverlaps(logger *slog.Logger, cmd string, overlaps []string) {
	for _, o := range overlaps {
		logger.Warn(cmd+": allowed files overlap", "entry", o)
	}
}

// readLine reads one line (the newline and a CR are not part of it).
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	s := strings.TrimRight(line, "\r\n")
	if s == "" {
		return "", errors.New("stdin: expected a line, got none")
	}
	return s, nil
}

// loadZone is --python-tz: an empty value is UTC.
func loadZone(zone string) (*time.Location, error) {
	if strings.TrimSpace(zone) == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(zone)
}

// writeReport writes the run's report. It never holds a credential.
func writeReport(path string, report any) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
