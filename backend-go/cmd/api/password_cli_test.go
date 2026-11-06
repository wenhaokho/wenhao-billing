package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sys/unix"
)

func buildAPI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api")
	cmd := exec.Command("go", "build", "-o", path, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %s", out)
	}
	return path
}

func userSnapshot(t *testing.T, pool *pgxpool.Pool) []json.RawMessage {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT row_to_json(u) FROM users u ORDER BY user_id`)
	if err != nil {
		t.Fatal("snapshot query failed")
	}
	defer rows.Close()
	var result []json.RawMessage
	for rows.Next() {
		var row json.RawMessage
		if err := rows.Scan(&row); err != nil {
			t.Fatal("snapshot scan failed")
		}
		result = append(result, row)
	}
	if rows.Err() != nil {
		t.Fatal("snapshot read failed")
	}
	return result
}

func runInteractive(t *testing.T, binary, dbURL, email string, passwords []string) (string, error) {
	t.Helper()
	cmd := exec.Command(binary, "set-password", email)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dbURL)
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	data := make(chan byte, 1024)
	go func() {
		b := make([]byte, 1)
		for {
			n, e := terminal.Read(b)
			if n > 0 {
				data <- b[0]
			}
			if e != nil {
				close(data)
				return
			}
		}
	}()
	var output bytes.Buffer
	readUntil := func(prompt string) {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for !strings.Contains(output.String(), prompt) {
			select {
			case b, ok := <-data:
				if !ok {
					t.Fatal("terminal closed before prompt")
				}
				output.WriteByte(b)
			case <-timer.C:
				t.Fatal("terminal prompt timed out")
			}
		}
	}
	for i, p := range passwords {
		prompt := "Password: "
		if i == 1 {
			prompt = "Confirm password: "
		}
		readUntil(prompt)
		// ReadPassword changes terminal settings after printing the prompt.
		deadline := time.Now().Add(3 * time.Second)
		for {
			state, e := unix.IoctlGetTermios(int(terminal.Fd()), unix.TCGETS)
			if e != nil {
				t.Fatal("cannot inspect terminal")
			}
			if state.Lflag&unix.ECHO == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("password input echoes")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if _, err := io.WriteString(terminal, p+"\n"); err != nil {
			t.Fatal("terminal input failed")
		}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("command timed out")
	}
	for b := range data {
		output.WriteByte(b)
	}
	for _, p := range passwords {
		if strings.Contains(output.String(), p) {
			t.Fatal("password appeared in terminal output")
		}
	}
	return output.String(), waitErr
}

func TestActualCLIPipeAndArguments(t *testing.T) {
	binary := buildAPI(t)
	for _, tc := range []struct {
		name        string
		args        []string
		input, want string
	}{
		{"pipe", []string{"set-password", "one@example.com"}, "synthetic-password\n", "interactive terminal"},
		{"argument", []string{"set-password", "one@example.com", "synthetic-password"}, "", "usage:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binary, tc.args...)
			cmd.Stdin = strings.NewReader(tc.input)
			out, err := cmd.CombinedOutput()
			if err == nil || !bytes.Contains(out, []byte(tc.want)) {
				t.Fatalf("expected %s rejection", tc.want)
			}
			if bytes.Contains(out, []byte("synthetic-password")) {
				t.Fatal("password appeared in output")
			}
		})
	}
}

func equalSnapshots(t *testing.T, a, b []json.RawMessage) {
	t.Helper()
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatal("user rows changed")
	}
}
