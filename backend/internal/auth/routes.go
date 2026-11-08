package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/observe"

	"github.com/wenhaokho/wenhao-billing/backend/internal/auth/authdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// resetTTL is how long an emailed reset link works.
const resetTTL = time.Hour

// Python's sentences.
const (
	invalidCredentials = "invalid credentials" //nolint:gosec // an error sentence, not a credential
	wrongCurrent       = "current password is incorrect"
	samePassword       = "new password must differ from current password"
	invalidResetToken  = "invalid or expired reset token" //nolint:gosec // an error sentence, not a token
	forgotMessage      = "If an account exists for that email, a reset link has been sent."
)

func errStatus(status int, detail string) error {
	return platform.Err(status, apierr.Codes[status], detail)
}

type routes struct {
	pool     *pgxpool.Pool
	q        *authdb.Queries
	sessions platform.Sessions
	mail     email.Sender
	cfg      platform.Config
	clock    platform.Clock
	throttle throttle
	// dummy is verified against when there is no argon2id hash to check (an
	// unknown email, a Python-era bcrypt hash), so every refused login costs
	// one argon2id verification, as a wrong password does.
	dummy string
}

// Register adds the /api/v1/auth operations. Every time decision (session
// expiry, reset expiry, throttle window) reads clock; a Sessions without a
// Clock gets this one.
func Register(api huma.API, pool *pgxpool.Pool, s platform.Sessions, mail email.Sender, cfg platform.Config, clock platform.Clock) {
	if clock == nil {
		clock = platform.SystemClock{}
	}
	if s.Clock == nil {
		s.Clock = clock
	}
	dummy, err := hashPassword("not-a-password")
	if err != nil {
		panic("auth: cannot build the dummy password hash: " + err.Error())
	}
	q := authdb.New(pool)
	r := routes{pool: pool, q: q, sessions: s, mail: mail, cfg: cfg, clock: clock, throttle: throttle{q: q}, dummy: dummy}
	public := []string{platform.PublicTag, "auth"}
	const prefix = "/api/v1/auth"

	huma.Register(api, huma.Operation{
		OperationID: "auth-login", Method: http.MethodPost, Path: prefix + "/login", Tags: public,
		Summary: "Sign in", Errors: []int{http.StatusUnauthorized, http.StatusTooManyRequests},
	}, r.login)
	huma.Register(api, huma.Operation{
		OperationID: "auth-logout", Method: http.MethodPost, Path: prefix + "/logout", Tags: public,
		Summary: "Sign out", DefaultStatus: http.StatusNoContent,
	}, r.logout)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "auth-me", Method: http.MethodGet, Path: prefix + "/me", Tags: []string{"auth"},
		Summary: "The signed-in user",
	}), r.me)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "auth-change-password", Method: http.MethodPost, Path: prefix + "/change-password", Tags: []string{"auth"},
		Summary: "Change the signed-in user's password", Errors: []int{http.StatusBadRequest},
	}), r.changePassword)
	huma.Register(api, huma.Operation{
		OperationID: "auth-forgot-password", Method: http.MethodPost, Path: prefix + "/forgot-password", Tags: public,
		Summary: "Email a password reset link",
	}, r.forgotPassword)
	huma.Register(api, huma.Operation{
		OperationID: "auth-reset-password", Method: http.MethodPost, Path: prefix + "/reset-password", Tags: public,
		Summary: "Set a new password with an emailed token", Errors: []int{http.StatusBadRequest},
	}, r.resetPassword)
}

// headerSink collects the headers a cookie helper sets: Huma handlers have
// no ResponseWriter, so cookies travel in the output's Set-Cookie field.
type headerSink http.Header

func (h headerSink) Header() http.Header       { return http.Header(h) }
func (headerSink) Write(b []byte) (int, error) { return len(b), nil }
func (headerSink) WriteHeader(int)             {}

func setCookies(write func(http.ResponseWriter)) []string {
	h := http.Header{}
	write(headerSink(h))
	return h.Values("Set-Cookie")
}

type loginIn struct {
	Body LoginRequest
	addr string
}

// Resolve reads the client address the throttle keys on.
func (in *loginIn) Resolve(ctx huma.Context) []error {
	in.addr = clientAddress(ctx.Header("CF-Connecting-IP"), ctx.Header("X-Forwarded-For"), ctx.RemoteAddr())
	return nil
}

type sessionOut struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      UserOut
}

func (r routes) issue(u authdb.User, remember bool) (*sessionOut, error) {
	token, maxAge, err := r.sessions.Issue(u.UserID, int(u.SessionEpoch), remember)
	if err != nil {
		return nil, err
	}
	return &sessionOut{
		SetCookie: setCookies(func(w http.ResponseWriter) { r.sessions.SetCookie(w, token, maxAge) }),
		Body:      userOut(u),
	}, nil
}

func (r routes) login(ctx context.Context, in *loginIn) (*sessionOut, error) {
	account := normalizeEmail(in.Body.Email)
	now := r.clock.Now()
	if err := r.throttle.attempt(ctx, account, in.addr, now); err != nil {
		return nil, err
	}
	u, err := r.q.UserByEmail(ctx, account)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, _ = verifyHash(in.Body.Password, r.dummy)
	case err != nil:
		return nil, err
	case r.verify(ctx, in.Body.Password, u.PasswordHash):
		if err := r.throttle.succeed(ctx, account, in.addr); err != nil {
			return nil, err
		}
		// No role check here, as in Python: a non-admin signs in and is
		// refused by every admin route.
		return r.issue(u, in.Body.Remember)
	}
	// The attempt's row stays: it counts as a failure.
	return nil, errStatus(http.StatusUnauthorized, invalidCredentials)
}

// verify reports whether password matches encoded. A hash that is not
// argon2id (Python's bcrypt, say) never matches and is never an error: the
// user is refused like a wrong password until set-password replaces it, and
// the dummy is verified instead so the refusal takes as long.
func (r routes) verify(ctx context.Context, password, encoded string) bool {
	ok, err := verifyHash(password, encoded)
	if err != nil {
		observe.LoggerFrom(ctx).Warn("password hash is not argon2id; run set-password for this user", "err", err)
		_, _ = verifyHash(password, r.dummy)
		return false
	}
	return ok
}

type logoutOut struct {
	SetCookie []string `header:"Set-Cookie"`
}

func (r routes) logout(context.Context, *struct{}) (*logoutOut, error) {
	return &logoutOut{SetCookie: setCookies(r.sessions.ClearCookie)}, nil
}

type userBody struct{ Body UserOut }

func (r routes) me(ctx context.Context, _ *struct{}) (*userBody, error) {
	u, err := r.q.UserByID(ctx, platform.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return &userBody{Body: userOut(u)}, nil
}

type changePasswordIn struct{ Body ChangePasswordRequest }

// changePassword signs every other session out (the epoch moves on) and
// re-issues this browser's cookie, remembered if it was. A wrong current
// password is 400, never 401: the frontend signs out on any 401.
func (r routes) changePassword(ctx context.Context, in *changePasswordIn) (*sessionOut, error) {
	p := platform.PrincipalFrom(ctx)
	u, err := r.q.UserByID(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !r.verify(ctx, in.Body.CurrentPassword, u.PasswordHash) {
		return nil, errStatus(http.StatusBadRequest, wrongCurrent)
	}
	if in.Body.CurrentPassword == in.Body.NewPassword {
		return nil, errStatus(http.StatusBadRequest, samePassword)
	}
	hash, err := hashPassword(in.Body.NewPassword)
	if err != nil {
		return nil, err
	}
	u, err = r.q.SetPassword(ctx, authdb.SetPasswordParams{UserID: u.UserID, PasswordHash: hash})
	if err != nil {
		return nil, err
	}
	return r.issue(u, p.Remember)
}

type forgotPasswordIn struct{ Body ForgotPasswordRequest }

type forgotPasswordOut struct{ Body ForgotPasswordResponse }

// forgotPassword answers the same for every address, so it cannot be used to
// probe for accounts. The database keeps only the token's hash; the token
// itself is only ever emailed.
func (r routes) forgotPassword(ctx context.Context, in *forgotPasswordIn) (*forgotPasswordOut, error) {
	generic := &forgotPasswordOut{Body: ForgotPasswordResponse{Message: forgotMessage}}
	u, err := r.q.UserByEmail(ctx, normalizeEmail(in.Body.Email))
	if errors.Is(err, pgx.ErrNoRows) {
		return generic, nil
	}
	if err != nil {
		return nil, err
	}
	token, err := newResetToken()
	if err != nil {
		return nil, err
	}
	expires := r.clock.Now().UTC().Add(resetTTL)
	if err := r.q.SetResetToken(ctx, authdb.SetResetTokenParams{UserID: u.UserID, TokenHash: tokenHash(token), Expires: expires}); err != nil {
		return nil, err
	}
	link := strings.TrimRight(r.cfg.AppBaseURL, "/") + "/reset-password?token=" + token
	// A failed send is not the caller's business: the token stays valid.
	if err := r.mail.Send(ctx, resetEmail(u.Email, link)); err != nil {
		observe.LoggerFrom(ctx).Error("password reset email failed", "err", err)
	}
	return generic, nil
}

// resetEmail is Python's send_password_reset_email message.
func resetEmail(to, link string) email.Message {
	return email.Message{
		To:      to,
		Subject: "Reset your wenhao-billing password",
		Text: "Hi,\n\n" +
			"A password reset was requested for your wenhao-billing account.\n" +
			"Open the link below within the next hour to choose a new password:\n\n" +
			link + "\n\n" +
			"If you didn't request this, you can ignore this email — your current " +
			"password will stay active.\n",
		HTML: "<p>Hi,</p>" +
			"<p>A password reset was requested for your wenhao-billing account.</p>" +
			`<p><a href="` + link + `">Reset your password</a> ` +
			"(link expires in 1 hour).</p>" +
			"<p>If you didn't request this, you can ignore this email.</p>",
	}
}

type resetPasswordIn struct{ Body ResetPasswordRequest }

// testHookResetLocked, when a test sets it, runs once the reset token's row
// is read (and locked) inside the transaction. Always nil in production.
var testHookResetLocked func()

// resetPassword consumes a reset token: unknown, used and expired tokens are
// all one 400, and an expired one is cleared. The row is locked, so a token
// is spent once even by two requests at the same moment.
func (r routes) resetPassword(ctx context.Context, in *resetPasswordIn) (*userBody, error) {
	var (
		out     authdb.User
		invalid bool
	)
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		row, err := q.UserByResetTokenHash(ctx, tokenHash(in.Body.Token))
		if errors.Is(err, pgx.ErrNoRows) {
			invalid = true
			return nil
		}
		if err != nil {
			return err
		}
		if testHookResetLocked != nil {
			testHookResetLocked()
		}
		if row.PasswordResetExpires == nil || row.PasswordResetExpires.Before(r.clock.Now().UTC()) {
			invalid = true
			return q.ClearResetToken(ctx, row.UserID)
		}
		hash, err := hashPassword(in.Body.NewPassword)
		if err != nil {
			return err
		}
		out, err = q.SetPassword(ctx, authdb.SetPasswordParams{UserID: row.UserID, PasswordHash: hash})
		return err
	})
	if err != nil {
		return nil, err
	}
	if invalid {
		return nil, errStatus(http.StatusBadRequest, invalidResetToken)
	}
	return &userBody{Body: userOut(out)}, nil
}
