// Package auth serves /api/v1/auth with the Python backend's contract: login
// and logout on the billing_session cookie, the signed-in user, password
// change, and the emailed password reset. It also throttles failed logins
// and holds the set-password and issue-session commands.
package auth

import (
	"time"

	"github.com/google/uuid"

	gauth "github.com/LOQATO/gokit/auth"

	"github.com/wenhaokho/wenhao-billing/backend/internal/auth/authdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// HashParams are the argon2id parameters for new password hashes (OWASP's
// 19 MiB, two passes, one lane).
var HashParams = gauth.HashParams{Memory: 19456, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}

// UserOut is the signed-in user, as /auth/login, /auth/me and the password
// routes answer.
type UserOut struct {
	UserID      uuid.UUID     `json:"user_id"`
	Email       string        `json:"email"`
	Role        string        `json:"role"`
	DisplayName *string       `json:"display_name"`
	CreatedAt   platform.Time `json:"created_at"`
}

func userOut(u authdb.User) UserOut {
	// created_at is naive UTC with a default; it is never null in practice.
	var created time.Time
	if u.CreatedAt != nil {
		created = *u.CreatedAt
	}
	return UserOut{UserID: u.UserID, Email: u.Email, Role: u.Role, DisplayName: u.DisplayName, CreatedAt: platform.Time{Time: created}}
}

// LoginRequest signs in. Remember keeps the session for the remember TTL
// (30 days by default) instead of the short one (8 hours).
type LoginRequest struct {
	Email    string `json:"email" format:"email"`
	Password string `json:"password"`
	Remember bool   `json:"remember,omitempty"`
}

// ChangePasswordRequest changes the signed-in user's password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password" minLength:"6" maxLength:"128"`
}

// ForgotPasswordRequest asks for a reset link.
type ForgotPasswordRequest struct {
	Email string `json:"email" format:"email"`
}

// ForgotPasswordResponse is the same for every address. The token and link
// are always null: they are only ever emailed.
type ForgotPasswordResponse struct {
	Message    string  `json:"message"`
	ResetToken *string `json:"reset_token"`
	ResetLink  *string `json:"reset_link"`
}

// ResetPasswordRequest sets a new password with an emailed token.
type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password" minLength:"6" maxLength:"128"`
}
