package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestAPIStartupRejectsUnsupportedDatabase(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	start := func() (*exec.Cmd, <-chan string, string) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		cmd := exec.Command(os.Args[0], "-test.run=^TestAPIStartupHelper$")
		cmd.Env = append(os.Environ(), "TASK70_API_HELPER=1", "DATABASE_URL="+db.URL(), fmt.Sprintf("PORT=%d", port), "SESSION_SIGNING_SECRET=0123456789abcdef0123456789abcdef", "SCHEDULER_ENABLED=true", "SMTP_HOST=", "RESEND_API_KEY=", "CLOUDFLARE_API_TOKEN=")
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = cmd.Stdout
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		output := make(chan string, 1)
		go func() { b, _ := io.ReadAll(pipe); output <- string(b) }()
		return cmd, output, fmt.Sprintf("http://127.0.0.1:%d/health", port)
	}
	cmd, output, health := start()
	up := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		resp, e := http.Get(health)
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				up = true
				break
			}
		}
	}
	if !up {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("current database did not start: %s", <-output)
	}
	cmd.Process.Signal(os.Interrupt)
	if err = cmd.Wait(); err != nil {
		t.Fatalf("shutdown: %v %s", err, <-output)
	}
	<-output
	for _, tc := range []struct{ name, change, restore string }{
		{"older", `DELETE FROM goose_db_version WHERE version_id=5`, `INSERT INTO goose_db_version(id,version_id,is_applied,tstamp) VALUES (6,5,true,now())`},
		{"alembic", `CREATE TABLE alembic_version(version_num text)`, `DROP TABLE alembic_version`},
		{"drift", `ALTER TABLE invoices ADD COLUMN task70_drift text`, `ALTER TABLE invoices DROP COLUMN task70_drift`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err = p.Exec(context.Background(), tc.change); err != nil {
				t.Fatal(err)
			}
			defer p.Exec(context.Background(), tc.restore)
			var before string
			if err = p.QueryRow(context.Background(), `SELECT jsonb_agg(to_jsonb(x) ORDER BY id)::text FROM goose_db_version x`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			cmd, out, health := start()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("unsupported database started")
				}
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-done
				t.Fatal("unsupported database remained running")
			}
			text := <-out
			if strings.Contains(text, "worker unavailable") {
				t.Fatalf("workers initialized: %s", text)
			}
			if resp, e := http.Get(health); e == nil {
				resp.Body.Close()
				t.Fatal("unsupported database opened listener")
			}
			var after string
			if err = p.QueryRow(context.Background(), `SELECT jsonb_agg(to_jsonb(x) ORDER BY id)::text FROM goose_db_version x`).Scan(&after); err != nil || before != after {
				t.Fatalf("metadata changed: %v", err)
			}
		})
	}
}
func TestAPIStartupHelper(t *testing.T) {
	if os.Getenv("TASK70_API_HELPER") != "1" {
		return
	}
	os.Args = []string{"api"}
	if err := run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestAPIPoolUsesVerifiedSchema(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	raw, err := url.Parse(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	q := raw.Query()
	q.Set("search_path", "pg_catalog")
	raw.RawQuery = q.Encode()
	pool, err := platform.OpenDB(context.Background(), raw.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var path string
	if err = pool.QueryRow(context.Background(), `SHOW search_path`).Scan(&path); err != nil || path != "public" {
		t.Fatalf("unverified pool search path %q: %v", path, err)
	}
}
