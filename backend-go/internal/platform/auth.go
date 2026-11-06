package platform

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
)

type SessionConfig struct {
	Secret            []byte
	Path              string
	Secure            bool
	SameSite          http.SameSite
	Sender            email.Sender
	BaseURL           string
	BaseCurrency      string
	FXProviderURL     string
	FXSyncCurrencies  []string
	FXProviderTimeout time.Duration
	hashParams        *auth.HashParams // nil uses production defaults; tests may inject lower cost
}

func (c SessionConfig) validate() error {
	if c.BaseCurrency != "" && utf8.RuneCountInString(c.BaseCurrency) != 3 {
		return errors.New("invalid BASE_CURRENCY")
	}
	if c.FXProviderTimeout < 0 {
		return errors.New("invalid FX_PROVIDER_TIMEOUT_SECONDS")
	}
	if c.FXProviderURL != "" {
		u, err := url.Parse(c.FXProviderURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid FX_PROVIDER_BASE_URL")
		}
	}
	if len(c.Secret) < 32 {
		return errors.New("SESSION_SIGNING_SECRET must be at least 32 bytes")
	}
	if !strings.HasPrefix(c.Path, "/") || strings.ContainsAny(c.Path, ";\r\n") {
		return errors.New("invalid session cookie path")
	}
	if c.SameSite != http.SameSiteLaxMode && c.SameSite != http.SameSiteStrictMode && c.SameSite != http.SameSiteNoneMode {
		return errors.New("invalid session SameSite")
	}
	if c.SameSite == http.SameSiteNoneMode && !c.Secure {
		return errors.New("SameSite=None requires Secure")
	}
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid APP_BASE_URL")
		}
	}
	return nil
}

type sessionHandler struct {
	db     *pgxpool.Pool
	config SessionConfig
}

func (s sessionHandler) cookie(w http.ResponseWriter, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: value, Path: s.config.Path, Secure: s.config.Secure, HttpOnly: true, SameSite: s.config.SameSite, MaxAge: age})
}
func authError(w http.ResponseWriter, code int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}
func userJSON(w http.ResponseWriter, id pgtype.UUID, email, role string, name pgtype.Text, created pgtype.Timestamp) {
	var display *string
	if name.Valid {
		display = &name.String
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		UserID      pgtype.UUID `json:"user_id"`
		Email       string      `json:"email"`
		Role        string      `json:"role"`
		DisplayName *string     `json:"display_name"`
		CreatedAt   time.Time   `json:"created_at"`
	}{id, email, role, display, created.Time})
}
func (s sessionHandler) login(w http.ResponseWriter, r *http.Request) {
	in, valid := loginValidation(w, r)
	if !valid {
		return
	}
	if s.db == nil {
		authError(w, 500, "internal server error")
		return
	}
	user, err := dbquery.New(s.db).GetSessionUserByEmail(r.Context(), in.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 401, "invalid credentials")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	ok, err := auth.Verify(in.Password, user.PasswordHash)
	if err != nil || !ok {
		authError(w, 401, "invalid credentials")
		return
	}
	ttl := 8 * time.Hour
	if in.Remember {
		ttl = 30 * 24 * time.Hour
	}
	token, err := (auth.Tokens{Secret: s.config.Secret, AccessTTL: ttl}).Issue(jwt.MapClaims{"sub": user.UserID.String()})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	s.cookie(w, token, int(ttl.Seconds()))
	userJSON(w, user.UserID, user.Email, user.Role, user.DisplayName, user.CreatedAt)
}
func (s sessionHandler) sessionID(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	c, err := r.Cookie("session")
	if err != nil {
		authError(w, 401, "not authenticated")
		return pgtype.UUID{}, false
	}
	claims, err := (auth.Tokens{Secret: s.config.Secret}).Parse(c.Value)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			authError(w, 401, "session expired")
		} else {
			authError(w, 401, "not authenticated")
		}
		return pgtype.UUID{}, false
	}
	sub, err := claims.GetSubject()
	var id pgtype.UUID
	if err != nil || id.Scan(sub) != nil || !id.Valid {
		authError(w, 401, "not authenticated")
		return pgtype.UUID{}, false
	}
	return id, true
}
func (s sessionHandler) requireAdmin(w http.ResponseWriter, r *http.Request) (dbquery.GetSessionUserByIDRow, bool) {
	id, ok := s.sessionID(w, r)
	if !ok {
		return dbquery.GetSessionUserByIDRow{}, false
	}
	if s.db == nil {
		authError(w, 500, "internal server error")
		return dbquery.GetSessionUserByIDRow{}, false
	}
	user, err := dbquery.New(s.db).GetSessionUserByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return user, false
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return user, false
	}
	return user, true
}
func (s sessionHandler) me(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	userJSON(w, user.UserID, user.Email, user.Role, user.DisplayName, user.CreatedAt)
}
func (s sessionHandler) logout(w http.ResponseWriter, _ *http.Request) {
	s.cookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
