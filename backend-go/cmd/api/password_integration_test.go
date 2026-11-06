package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestSetPasswordDatabase(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	db := pgtestdb.Custom(t, cfg, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	config, err := pgxpool.ParseConfig(db.URL())
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	_, err = pool.Exec(ctx, `INSERT INTO users (user_id,email,password_hash,password_reset_token,password_reset_expires,display_name) VALUES ('00000000-0000-0000-0000-000000000001','one@example.com','old','token',now(),'One'),('00000000-0000-0000-0000-000000000002','two@example.com','other','token2',now(),'Two')`)
	if err != nil {
		t.Fatal(err)
	}
	// A database failure must roll back the transaction and preserve all fields.
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_password() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$; CREATE TRIGGER reject_password BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION reject_password()`)
	if err != nil {
		t.Fatal(err)
	}
	if setPassword(ctx, pool, "one@example.com", "secret1", auth.DefaultHashParams) == nil {
		t.Fatal("update failure accepted")
	}
	var beforeHash, beforeToken string
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := pool.QueryRow(checkCtx, `SELECT password_hash,password_reset_token FROM users WHERE email='one@example.com'`).Scan(&beforeHash, &beforeToken); err != nil || beforeHash != "old" || beforeToken != "token" {
		t.Fatal("failure changed user")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_password ON users`); err != nil {
		t.Fatal(err)
	}
	binary := buildAPI(t)
	original := userSnapshot(t, pool)
	for _, tc := range []struct {
		email     string
		passwords []string
		want      string
	}{
		{"unknown@example.com", []string{"synthetic-pass-one", "synthetic-pass-one"}, "user not found"},
		{"one@example.com", []string{"short", "short"}, "6 to 128"},
		{"one@example.com", []string{"synthetic-pass-one", "synthetic-pass-two"}, "passwords do not match"},
	} {
		out, err := runInteractive(t, binary, db.URL(), tc.email, tc.passwords)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Fatalf("expected %s rejection", tc.want)
		}
		equalSnapshots(t, original, userSnapshot(t, pool))
	}
	out, err := runInteractive(t, binary, db.URL(), "one@example.com", []string{"synthetic-pass-one", "synthetic-pass-one"})
	if err != nil || !strings.Contains(out, "Password updated") {
		t.Fatal("interactive password update failed")
	}
	after := userSnapshot(t, pool)
	if len(original) != 2 || len(after) != 2 || string(original[1]) != string(after[1]) {
		t.Fatal("other user's row changed")
	}
	var beforeUser, afterUser map[string]any
	if json.Unmarshal(original[0], &beforeUser) != nil || json.Unmarshal(after[0], &afterUser) != nil {
		t.Fatal("invalid user snapshot")
	}
	for _, key := range []string{"password_hash", "password_reset_token", "password_reset_expires"} {
		delete(beforeUser, key)
		delete(afterUser, key)
	}
	if !reflect.DeepEqual(beforeUser, afterUser) {
		t.Fatal("other target user fields changed")
	}
	var cliHash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email='one@example.com'`).Scan(&cliHash); err != nil {
		t.Fatal(err)
	}
	if ok, _ := auth.Verify("synthetic-pass-one", cliHash); !ok {
		t.Fatal("interactive hash does not verify")
	}
	cheap := auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	for _, email := range []string{"wrong@example.com"} {
		if setPassword(ctx, pool, email, "secret1", cheap) == nil {
			t.Fatal("unknown email accepted")
		}
	}
	if err := setPassword(ctx, pool, "one@example.com", "secret1", cheap); err != nil {
		t.Fatal(err)
	}
	var hash, name string
	var token sql.NullString
	var expiry sql.NullTime
	if err := pool.QueryRow(ctx, `SELECT password_hash,display_name,password_reset_token,password_reset_expires FROM users WHERE email='one@example.com'`).Scan(&hash, &name, &token, &expiry); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatal("not argon2id")
	}
	if ok, _ := auth.Verify("secret1", hash); !ok {
		t.Fatal("hash does not verify")
	}
	if token.Valid || expiry.Valid || name != "One" {
		t.Fatal("reset fields or profile incorrect")
	}
	var other, otherToken string
	if err := pool.QueryRow(ctx, `SELECT password_hash,password_reset_token FROM users WHERE email='two@example.com'`).Scan(&other, &otherToken); err != nil || other != "other" || otherToken != "token2" {
		t.Fatal("other user changed")
	}
}
