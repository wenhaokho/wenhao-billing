// Command compare replays the frontend's calls against two backends and diffs
// their JSON, so the Go port can be checked side by side with the frozen
// Python backend before the switch-over.
//
//	compare reads --python URL --go URL --python-email E \
//	  [--python-password-stdin | --python-cookie-stdin] --go-token-stdin \
//	  --corpus FILE --allowed FILE [--python-tz ZONE] --out report.json
//
// Every corpus request is a GET. The only non-GET is Python's POST
// /auth/login, and only with --python-password-stdin; --python-cookie-stdin
// sends no POST at all. The Go token is sent as the billing_session cookie and
// the runner never POSTs to Go.
//
// Exit codes: 0 clean, 1 differences, 2 error.
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

func main() {
	if len(os.Args) < 2 {
		slog.Error("usage: compare reads --python URL --go URL --python-email E [--python-password-stdin | --python-cookie-stdin] --go-token-stdin --corpus FILE --allowed FILE [--python-tz ZONE] --out report.json")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "reads":
		os.Exit(readsCommand(os.Args[2:], os.Stdin))
	default:
		slog.Error("compare: unknown subcommand", "subcommand", os.Args[1])
		os.Exit(2)
	}
}

// readsCommand runs `compare reads`. The password, cookie and token come from
// stdin in that order (one line each), and none is ever logged.
func readsCommand(args []string, stdin io.Reader) int {
	fs := flag.NewFlagSet("reads", flag.ContinueOnError)
	var (
		pythonURL      = fs.String("python", "", "Python base URL")
		goURL          = fs.String("go", "", "Go base URL")
		pythonEmail    = fs.String("python-email", "", "Python admin email")
		pythonPassword = fs.Bool("python-password-stdin", false, "read the Python password from stdin (one line); sends one POST")
		pythonCookie   = fs.Bool("python-cookie-stdin", false, "read the Python billing_session cookie from stdin (one line); sends no POST")
		goToken        = fs.Bool("go-token-stdin", false, "read the Go session token from stdin (one line)")
		corpusFile     = fs.String("corpus", "", "the compare-corpus.yaml path")
		allowedFile    = fs.String("allowed", "", "the allowed-diffs.yaml path")
		pythonTZ       = fs.String("python-tz", "", "zone a naive Python timestamp is read in (default UTC)")
		outFile        = fs.String("out", "", "the report JSON path")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := checkReadFlags(*pythonURL, *goURL, *pythonEmail, *pythonPassword, *pythonCookie, *goToken, *corpusFile, *allowedFile, *outFile); err != nil {
		slog.Error("compare reads: " + err.Error())
		return 2
	}

	reader := bufio.NewReader(stdin)
	var password, cookie, token string
	var err error
	if *pythonPassword {
		if password, err = readLine(reader); err != nil {
			slog.Error("compare reads: reading the Python password", "err", err)
			return 2
		}
	}
	if *pythonCookie {
		if cookie, err = readLine(reader); err != nil {
			slog.Error("compare reads: reading the Python cookie", "err", err)
			return 2
		}
	}
	if token, err = readLine(reader); err != nil {
		slog.Error("compare reads: reading the Go token", "err", err)
		return 2
	}

	corpus, err := compare.LoadCorpusFile(*corpusFile)
	if err != nil {
		slog.Error("compare reads: loading the corpus", "err", err)
		return 2
	}
	allowed, err := compare.LoadAllowedFile(*allowedFile)
	if err != nil {
		slog.Error("compare reads: loading the allowed differences", "err", err)
		return 2
	}
	tz, err := loadZone(*pythonTZ)
	if err != nil {
		slog.Error("compare reads: --python-tz", "err", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	report, runErr := compare.RunReads(ctx, compare.ReadOptions{
		PythonURL: *pythonURL, GoURL: *goURL, PythonEmail: *pythonEmail,
		PythonPassword: password, PythonCookie: cookie, GoToken: token,
		Corpus: corpus, Allowed: allowed, PythonTZ: tz,
	})
	code := compare.ExitCode(report, runErr)
	if err := writeReport(*outFile, report); err != nil {
		slog.Error("compare reads: writing the report", "err", err)
		return 2
	}
	if runErr != nil {
		slog.Error("compare reads: the run failed", "err", runErr, "report", *outFile)
		return code
	}
	slog.Info("compare reads: done", "cases", len(report.Cases), "differences", report.Differences, "report", *outFile)
	return code
}

// checkReadFlags refuses a run with a missing or contradictory flag.
func checkReadFlags(pythonURL, goURL, email string, password, cookie, token bool, corpus, allowed, out string) error {
	switch {
	case pythonURL == "":
		return errors.New("--python is required")
	case goURL == "":
		return errors.New("--go is required")
	case email == "":
		return errors.New("--python-email is required")
	case corpus == "":
		return errors.New("--corpus is required")
	case allowed == "":
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
func writeReport(path string, report compare.Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}
