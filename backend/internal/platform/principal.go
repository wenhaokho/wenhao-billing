package platform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/auth"
)

// SessionCookie is the cookie the browser carries its session in, under the
// name the Python backend used.
const SessionCookie = "billing_session"

// PublicTag marks an operation that needs no signed-in principal.
const PublicTag = "public"

// sessionType is the typ claim of a session token. Other tokens signed with
// the same secret (a reset link, say) never open the API.
const sessionType = "session"

// Principal is the signed-in admin.
type Principal struct {
	UserID uuid.UUID
	Email  string
	Role   string
	// Remember is whether the session was signed in with "keep me logged
	// in"; a re-issued cookie (after a password change) keeps it.
	Remember bool
}

type principalKey struct{}

// PrincipalFrom is the signed-in caller. Every operation except a public one
// has one; calling this in a public operation returns the zero Principal.
func PrincipalFrom(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}

const adminKey = "admin"

// Admin marks an operation admin-only, as Python's current_admin dependency
// did. Every operation that is not public must use it: one that is neither is
// refused to everyone, and TestEveryOperationIsAdminOrPublic fails.
func Admin(op huma.Operation) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[adminKey] = true
	return op
}

// IsAdmin reports whether op was marked with Admin.
func IsAdmin(op *huma.Operation) bool {
	v, _ := op.Metadata[adminKey].(bool)
	return v
}

// IsPublic reports whether op is tagged PublicTag.
func IsPublic(op *huma.Operation) bool { return slices.Contains(op.Tags, PublicTag) }

// Sessions signs session tokens and sets the cookie that carries them.
type Sessions struct {
	Tokens                 auth.Tokens // HS256 with SESSION_SECRET; its Secret and Leeway are used
	Cookie                 auth.CookieSpec
	MaxAge, RememberMaxAge time.Duration
	// Clock times a token's iat and exp and its expiry check. Nil is the
	// system clock; NewServer gives it the server's Clock.
	Clock Clock
}

func (s Sessions) now() time.Time {
	if s.Clock == nil {
		return SystemClock{}.Now()
	}
	return s.Clock.Now()
}

// NewSessions is the session setup cfg describes. The cookie is Secure unless
// the API runs on local plain HTTP.
func NewSessions(cfg Config) Sessions {
	return Sessions{
		Tokens:         auth.Tokens{Secret: []byte(cfg.SessionSecret), Leeway: 10 * time.Second},
		Cookie:         auth.CookieSpec{Name: SessionCookie, Path: "/", Secure: !cfg.LocalHTTP()},
		MaxAge:         cfg.SessionMaxAge,
		RememberMaxAge: cfg.SessionRememberMaxAge,
	}
}

// Issue signs a session for the user at its current session epoch; it lasts
// RememberMaxAge when remember is set, otherwise MaxAge.
func (s Sessions) Issue(userID uuid.UUID, epoch int, remember bool) (token string, maxAge time.Duration, err error) {
	maxAge = s.MaxAge
	if remember {
		maxAge = s.RememberMaxAge
	}
	// Signed here rather than by auth.Tokens.Issue, which reads time.Now:
	// iat and exp come from the Clock, so tests can move time.
	now := s.now()
	claims := jwt.MapClaims{
		"sub": userID.String(), "typ": sessionType, "epoch": epoch, "remember": remember,
		"iat": now.Unix(), "exp": now.Add(maxAge).Unix(),
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.Tokens.Secret)
	if err != nil {
		return "", 0, fmt.Errorf("session: sign: %w", err)
	}
	return token, maxAge, nil
}

// parse verifies token as auth.Tokens.Parse does, on the Clock's time.
func (s Sessions) parse(token string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return s.Tokens.Secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithLeeway(s.Tokens.Leeway),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// SetCookie sends token in the session cookie, kept for maxAge.
func (s Sessions) SetCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	c := s.Cookie
	c.MaxAge = maxAge
	auth.SetCookie(w, c, token)
}

// ClearCookie tells the browser to drop the session cookie.
func (s Sessions) ClearCookie(w http.ResponseWriter) { auth.ClearCookie(w, s.Cookie) }

// headerWriter collects the headers a cookie helper sets, for a Huma context.
type headerWriter struct{ h http.Header }

func (w headerWriter) Header() http.Header       { return w.h }
func (headerWriter) Write(b []byte) (int, error) { return len(b), nil }
func (headerWriter) WriteHeader(int)             {}

// Python's sentences for a refused request.
const (
	notAuthenticated = "not authenticated"
	sessionExpired   = "session expired"
	adminRequired    = "admin required"
)

// authenticate is a Huma middleware: an operation tagged PublicTag is open;
// an Admin operation needs a live session of an admin; any other operation is
// refused to everyone (fail closed).
func authenticate(api huma.API, pool *pgxpool.Pool, s Sessions) func(huma.Context, func(huma.Context)) {
	refuse := func(ctx huma.Context, status int, sentence string) {
		_ = huma.WriteErr(api, ctx, status, "", Err(status, apierr.Codes[status], sentence))
	}
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if IsPublic(op) {
			next(ctx)
			return
		}
		if !IsAdmin(op) {
			refuse(ctx, http.StatusForbidden, adminRequired)
			return
		}
		userID, epoch, remember, err := s.read(ctx)
		if errors.Is(err, errSessionExpired) {
			s.expire(ctx)
			refuse(ctx, http.StatusUnauthorized, sessionExpired)
			return
		}
		if err != nil {
			refuse(ctx, http.StatusUnauthorized, notAuthenticated)
			return
		}
		p := Principal{UserID: userID, Remember: remember}
		var current int
		err = pool.QueryRow(ctx.Context(), `SELECT email, role, session_epoch FROM users WHERE user_id = $1`, userID).
			Scan(&p.Email, &p.Role, &current)
		if errors.Is(err, pgx.ErrNoRows) {
			refuse(ctx, http.StatusForbidden, adminRequired)
			return
		}
		if err != nil {
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "", err)
			return
		}
		// A session older than the user's epoch was signed out (password
		// change, sign out everywhere). A newer one is never issued
		// legitimately (a deleted-then-recreated user): epochs must match.
		if epoch != current {
			s.expire(ctx)
			refuse(ctx, http.StatusUnauthorized, sessionExpired)
			return
		}
		if p.Role != "admin" {
			refuse(ctx, http.StatusForbidden, adminRequired)
			return
		}
		next(huma.WithValue(ctx, principalKey{}, p))
	}
}

var (
	errNoSession      = errors.New("no session")
	errSessionExpired = errors.New("session expired")
)

// read is the user, epoch and remember flag the request's session cookie
// names.
func (s Sessions) read(ctx huma.Context) (userID uuid.UUID, epoch int, remember bool, err error) {
	c, err := huma.ReadCookie(ctx, SessionCookie)
	if err != nil {
		return uuid.Nil, 0, false, errNoSession
	}
	return s.readToken(c.Value)
}

// readToken is read on the cookie's value.
func (s Sessions) readToken(token string) (userID uuid.UUID, epoch int, remember bool, err error) {
	claims, err := s.parse(token)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return uuid.Nil, 0, false, errSessionExpired
	}
	if err != nil || claims["typ"] != sessionType {
		return uuid.Nil, 0, false, errNoSession
	}
	sub, _ := claims["sub"].(string)
	userID, err = uuid.Parse(sub)
	// JSON numbers arrive as float64; the epoch is a counter, not money.
	n, ok := claims["epoch"].(float64)                      //nolint:forbidigo // a JSON number, not money
	if err != nil || !ok || n < 0 || n != float64(int(n)) { //nolint:forbidigo // a JSON number, not money
		return uuid.Nil, 0, false, errNoSession
	}
	remember, _ = claims["remember"].(bool)
	return userID, int(n), remember, nil
}

// ErrNoLiveSession is a request with no live session: no cookie, a token that
// does not verify, one that has expired, or one signed at an older (or newer)
// session epoch than its user's.
var ErrNoLiveSession = errors.New("no live session")

// RequestPrincipal is the user r's session cookie signs in, checked as an
// Admin operation checks it (signature, expiry on the Clock, session epoch),
// for plain handlers outside Huma (the MCP connector's OAuth pages). A
// session whose user no longer exists is a Principal with an empty Role, so
// a role check refuses it. The caller checks the role.
func (s Sessions) RequestPrincipal(ctx context.Context, pool *pgxpool.Pool, r *http.Request) (Principal, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return Principal{}, ErrNoLiveSession
	}
	userID, epoch, remember, err := s.readToken(c.Value)
	if err != nil {
		return Principal{}, ErrNoLiveSession
	}
	p := Principal{UserID: userID, Remember: remember}
	var current int
	err = pool.QueryRow(ctx, `SELECT email, role, session_epoch FROM users WHERE user_id = $1`, userID).
		Scan(&p.Email, &p.Role, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{UserID: userID}, nil
	}
	if err != nil {
		return Principal{}, fmt.Errorf("session user: %w", err)
	}
	if epoch != current {
		return Principal{}, ErrNoLiveSession
	}
	return p, nil
}

// expire clears the session cookie on the answer, as Python cleared the
// session it found expired.
func (s Sessions) expire(ctx huma.Context) {
	h := http.Header{}
	s.ClearCookie(headerWriter{h})
	for _, v := range h.Values("Set-Cookie") {
		ctx.AppendHeader("Set-Cookie", v)
	}
}
