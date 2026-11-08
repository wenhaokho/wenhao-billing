package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	gauth "github.com/LOQATO/gokit/auth"

	"github.com/wenhaokho/wenhao-billing/backend/internal/auth/authdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Password length bounds, as Python's schemas had them.
const (
	minPassword = 6
	maxPassword = 128
)

// argonSlots bounds the argon2id runs (hashes and verifications) in flight:
// each takes HashParams.Memory (19 MiB), so thirty simultaneous logins would
// otherwise take some 570 MiB.
var argonSlots = make(chan struct{}, 2)

// withArgonSlot runs f once a slot is free, or returns ctx's error if ctx
// ends first.
func withArgonSlot(ctx context.Context, f func()) error {
	select {
	case argonSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-argonSlots }()
	f()
	return nil
}

func hashPassword(ctx context.Context, password string) (string, error) {
	var (
		hash string
		err  error
	)
	if slotErr := withArgonSlot(ctx, func() { hash, err = gauth.Hash(password, HashParams) }); slotErr != nil {
		return "", slotErr
	}
	return hash, err
}

// verifyHash checks a password against an encoded hash. A variable so a
// test can count the argon2id verifications a request runs. Call it only
// inside withArgonSlot.
var verifyHash = gauth.Verify

// normalizeEmail lowercases the domain, as Python's EmailStr did; the local
// part is kept as typed.
func normalizeEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return email
	}
	return email[:at] + strings.ToLower(email[at:])
}

// newResetToken is secrets.token_urlsafe(32): 32 random bytes, URL-safe
// base64 without padding (43 characters).
func newResetToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenHash is what the database keeps of a reset token: its SHA-256, hex.
// A leaked users table opens no account.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SetPassword gives the user with email a new argon2id password, signs every
// session of theirs out and voids any reset token. It is the set-password
// command, the way a Python-era (bcrypt) account gets back in.
func SetPassword(ctx context.Context, pool *pgxpool.Pool, email, password string) error {
	if n := utf8.RuneCountInString(password); n < minPassword || n > maxPassword {
		return fmt.Errorf("the password must be %d to %d characters", minPassword, maxPassword)
	}
	q := authdb.New(pool)
	u, err := q.UserByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return err
	}
	hash, err := hashPassword(ctx, password)
	if err != nil {
		return err
	}
	_, err = q.SetPassword(ctx, authdb.SetPasswordParams{UserID: u.UserID, PasswordHash: hash})
	return err
}

// IssueSession signs a short (not remembered) session for the admin with
// email, for scripts and tests against a running API. It writes nothing.
func IssueSession(ctx context.Context, pool *pgxpool.Pool, s platform.Sessions, email string) (string, error) {
	u, err := authdb.New(pool).UserByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no user with email %q", email)
	}
	if err != nil {
		return "", err
	}
	if u.Role != "admin" {
		return "", fmt.Errorf("%s is not an admin (role %q)", email, u.Role)
	}
	token, _, err := s.Issue(u.UserID, int(u.SessionEpoch), false)
	return token, err
}
