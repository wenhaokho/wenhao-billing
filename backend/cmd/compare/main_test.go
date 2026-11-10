package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReadsKeepsSecretsOutOfOutput drives the whole reads command with the two
// credentials on stdin (the Python password, then the Go token) and asserts
// neither secret reaches the report, stdout or stderr.
func TestReadsKeepsSecretsOutOfOutput(t *testing.T) {
	t.Parallel()
	const pySecret = "PY-PASSWORD-SECRET-7f3a"
	const goSecret = "GO-TOKEN-SECRET-91bd"

	// Both backends answer every route 200; the Python login sets a session
	// cookie ("py-session"), which is not a secret from stdin.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "billing_session", Value: "py-session", Path: "/"})
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	py := httptest.NewServer(handler)
	t.Cleanup(py.Close)
	gov := httptest.NewServer(handler)
	t.Cleanup(gov.Close)

	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.yaml")
	if err := os.WriteFile(corpusPath, []byte("cases:\n  - id: me\n    route: GET /api/v1/auth/me\n"), 0o600); err != nil {
		t.Fatalf("writing the corpus: %v", err)
	}
	allowedPath := filepath.Join(dir, "allowed.yaml")
	if err := os.WriteFile(allowedPath, []byte("[]\n"), 0o600); err != nil {
		t.Fatalf("writing the allowed file: %v", err)
	}
	outPath := filepath.Join(dir, "report.json")

	args := []string{
		"--python", py.URL, "--go", gov.URL,
		"--python-email", "owner@example.test",
		"--python-password-stdin", "--go-token-stdin",
		"--corpus", corpusPath, "--allowed", allowedPath, "--out", outPath,
	}
	stdin := strings.NewReader(pySecret + "\n" + goSecret + "\n")
	var stdout, stderr bytes.Buffer
	if code := readsCommand(args, commandIO{stdin: stdin, stdout: &stdout, stderr: &stderr}); code != 0 {
		t.Fatalf("readsCommand exit %d, want 0; stderr: %s", code, stderr.String())
	}
	report, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading the report: %v", err)
	}
	for name, blob := range map[string][]byte{
		"the report": report,
		"stdout":     stdout.Bytes(),
		"stderr":     stderr.Bytes(),
	} {
		for _, secret := range []string{pySecret, goSecret} {
			if bytes.Contains(blob, []byte(secret)) {
				t.Fatalf("%s contains the secret %q: %s", name, secret, blob)
			}
		}
	}
}

// TestReadsRefusesTerminalStdin pins that a run whose stdin is a terminal is
// refused before any secret is read.
func TestReadsRefusesTerminalStdin(t *testing.T) {
	t.Parallel()
	args := []string{
		"--python", "http://python.invalid", "--go", "http://go.invalid",
		"--python-email", "owner@example.test",
		"--python-password-stdin", "--go-token-stdin",
		"--corpus", "corpus.yaml", "--allowed", "allowed.yaml", "--out", "report.json",
	}
	var stdout, stderr bytes.Buffer
	code := readsCommand(args, commandIO{
		stdin:  fakeTerminal{strings.NewReader("pw\ntok\n")},
		stdout: &stdout,
		stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("exit %d, want 2 for a terminal stdin", code)
	}
	if !strings.Contains(stderr.String(), "do not type them into a terminal") {
		t.Fatalf("stderr did not refuse a terminal: %s", stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "pw") {
		t.Fatalf("a terminal run leaked stdin: %s %s", stdout.String(), stderr.String())
	}
}

// TestReadsHelpDocumentsCredentialOrder pins the --help text: the first stdin
// line is the Python credential, the second is the Go token.
func TestReadsHelpDocumentsCredentialOrder(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := readsCommand([]string{"-h"}, commandIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr})
	if code != 2 {
		t.Fatalf("exit %d, want 2 for -h", code)
	}
	help := stderr.String()
	if !strings.Contains(help, "first the Python credential") || !strings.Contains(help, "then the Go token") {
		t.Fatalf("-h does not document the credential order: %s", help)
	}
}

// fakeTerminal is an io.Reader whose Stat reports a character device, so
// isTerminal sees a keyboard.
type fakeTerminal struct {
	*strings.Reader
}

// Stat reports the reader as a terminal (os.ModeCharDevice).
func (fakeTerminal) Stat() (os.FileInfo, error) { return terminalInfo{}, nil }

// terminalInfo is the minimal os.FileInfo whose mode is a character device.
type terminalInfo struct{}

func (terminalInfo) Name() string       { return "tty" }
func (terminalInfo) Size() int64        { return 0 }
func (terminalInfo) Mode() os.FileMode  { return os.ModeCharDevice }
func (terminalInfo) ModTime() time.Time { return time.Time{} }
func (terminalInfo) IsDir() bool        { return false }
func (terminalInfo) Sys() any           { return nil }
