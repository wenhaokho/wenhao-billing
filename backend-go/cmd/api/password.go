package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/LOQATO/gokit/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"golang.org/x/term"
)

func validatePassword(password, confirmation string) error {
	n := utf8.RuneCountInString(password)
	if n < 6 || n > 128 {
		return errors.New("password must contain 6 to 128 characters")
	}
	if password != confirmation {
		return errors.New("passwords do not match")
	}
	return nil
}

func setPassword(ctx context.Context, pool *pgxpool.Pool, email, password string, params auth.HashParams) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errors.New("password reset failed")
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	user, err := q.FindUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("user not found")
	}
	if err != nil {
		return errors.New("password reset failed")
	}
	hash, err := auth.Hash(password, params)
	if err != nil {
		return errors.New("password reset failed")
	}
	_, err = q.SetUserPassword(ctx, dbquery.SetUserPasswordParams{UserID: user.UserID, PasswordHash: hash})
	if err != nil {
		return errors.New("password reset failed")
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("password reset failed")
	}
	return nil
}

// readSecret is injected only to permit tests to exercise the command without a terminal.
func runPasswordCommand(args []string, output io.Writer, interactive bool, readSecret func() (string, error)) error {
	if len(args) != 2 || args[0] != "set-password" {
		return errors.New("usage: api set-password <email>")
	}
	if !interactive {
		return errors.New("set-password requires an interactive terminal")
	}
	fmt.Fprint(output, "Password: ")
	password, err := readSecret()
	fmt.Fprintln(output)
	if err != nil {
		return errors.New("could not read password")
	}
	fmt.Fprint(output, "Confirm password: ")
	confirmation, err := readSecret()
	fmt.Fprintln(output)
	if err != nil {
		return errors.New("could not read password confirmation")
	}
	if err := validatePassword(password, confirmation); err != nil {
		return err
	}
	pool, err := platform.OpenDB(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := setPassword(context.Background(), pool, args[1], password, auth.DefaultHashParams); err != nil {
		return err
	}
	fmt.Fprintln(output, "Password updated")
	return nil
}

func passwordCommand(args []string) error {
	fd := int(os.Stdin.Fd())
	return runPasswordCommand(args, os.Stderr, term.IsTerminal(fd), func() (string, error) { p, err := term.ReadPassword(fd); return string(p), err })
}
