package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const usersPath = "/api/v1/users"

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// adminApp is an app with a signed-in admin; its request cookie is returned.
func adminApp(t *testing.T) (app, string) {
	t.Helper()
	a := newApp(t)
	a.user(t, adminEmail, "admin")
	return a, a.mustLogin(t, adminEmail, false)
}

func TestListUsersOrderedByEmail(t *testing.T) {
	a, c := adminApp(t)
	a.user(t, "zed@example.com", "admin")
	a.user(t, "bob@example.com", "admin")
	rec := a.do(t, http.MethodGet, usersPath, "", "Cookie", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var emails []string
	for _, u := range got {
		emails = append(emails, u["email"].(string))
	}
	want := []string{"bob@example.com", adminEmail, "zed@example.com"}
	if strings.Join(emails, ",") != strings.Join(want, ",") {
		t.Errorf("emails %v, want %v", emails, want)
	}
}

func TestUsersRequireAdmin(t *testing.T) {
	a := newApp(t)
	a.user(t, "member@example.com", "member")
	c := a.mustLogin(t, "member@example.com", false)
	if rec := a.do(t, http.MethodGet, usersPath, "", "Cookie", c); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d %s", rec.Code, rec.Body)
	}
	if rec := a.do(t, http.MethodGet, usersPath, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d %s", rec.Code, rec.Body)
	}
}

func patchMe(t *testing.T, a app, c string, body any) (code int, out map[string]any, raw string) {
	t.Helper()
	rec := a.do(t, http.MethodPatch, usersPath+"/me", jsonBody(t, body), "Cookie", c)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func TestUpdateMeDisplayNameAndEmail(t *testing.T) {
	a, c := adminApp(t)
	code, out, raw := patchMe(t, a, c, map[string]any{"display_name": "Wen", "email": "New@Example.COM"})
	if code != http.StatusOK || out["display_name"] != "Wen" || out["email"] != "New@example.com" {
		t.Fatalf("%d %s", code, raw)
	}
	// The session survives: the epoch does not move.
	if rec := a.me(t, c); rec.Code != http.StatusOK {
		t.Errorf("me after edit: %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateMeEmptyDisplayNameIsNull(t *testing.T) {
	a, c := adminApp(t)
	patchMe(t, a, c, map[string]any{"display_name": "Wen"})
	code, out, raw := patchMe(t, a, c, map[string]any{"display_name": ""})
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	if v, ok := out["display_name"]; !ok || v != nil {
		t.Errorf("display_name %v (present %v), want null", v, ok)
	}
}

func TestUpdateMeNullLeavesFields(t *testing.T) {
	a, c := adminApp(t)
	patchMe(t, a, c, map[string]any{"display_name": "Wen"})
	code, out, raw := patchMe(t, a, c, map[string]any{"display_name": nil, "email": nil})
	if code != http.StatusOK || out["display_name"] != "Wen" || out["email"] != adminEmail {
		t.Fatalf("%d %s", code, raw)
	}
	code, out, raw = patchMe(t, a, c, map[string]any{})
	if code != http.StatusOK || out["display_name"] != "Wen" || out["email"] != adminEmail {
		t.Fatalf("empty body: %d %s", code, raw)
	}
}

func TestUpdateMeEmailInUseIs409(t *testing.T) {
	a, c := adminApp(t)
	a.user(t, "taken@example.com", "admin")
	rec := a.do(t, http.MethodPatch, usersPath+"/me", jsonBody(t, map[string]any{"email": "taken@example.com"}), "Cookie", c)
	wantError(t, rec, http.StatusConflict, "email already in use")
	if out := bodyOf(t, a.me(t, c)); out["email"] != adminEmail {
		t.Errorf("email %v after a refused edit", out["email"])
	}
}

func TestUpdateMeSameEmailIsNoConflict(t *testing.T) {
	a, c := adminApp(t)
	code, out, raw := patchMe(t, a, c, map[string]any{"email": adminEmail})
	if code != http.StatusOK || out["email"] != adminEmail {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestUpdateMeInvalidEmailIs422(t *testing.T) {
	a, c := adminApp(t)
	code, _, raw := patchMe(t, a, c, map[string]any{"email": "not-an-email"})
	if code != http.StatusUnprocessableEntity || !strings.Contains(raw, "value is not a valid email address") {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestCreateUser201ThenLogin(t *testing.T) {
	a, c := adminApp(t)
	rec := a.do(t, http.MethodPost, usersPath,
		jsonBody(t, map[string]any{"email": "Fresh@Example.com", "password": "fresh-pass", "display_name": "Fresh"}), "Cookie", c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	out := bodyOf(t, rec)
	if out["email"] != "Fresh@example.com" || out["role"] != "admin" || out["display_name"] != "Fresh" || out["user_id"] == nil {
		t.Errorf("body %v", out)
	}
	if _, leaked := out["password_hash"]; leaked {
		t.Error("password_hash in the response")
	}
	if rec := a.login(t, "Fresh@example.com", "fresh-pass", false); rec.Code != http.StatusOK {
		t.Errorf("new user cannot sign in: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateUserDuplicateIs409(t *testing.T) {
	a, c := adminApp(t)
	rec := a.do(t, http.MethodPost, usersPath, jsonBody(t, map[string]any{"email": adminEmail, "password": "whatever1"}), "Cookie", c)
	wantError(t, rec, http.StatusConflict, "email already exists")
}

func TestCreateUserPasswordLengthIs422(t *testing.T) {
	a, c := adminApp(t)
	for _, pw := range []string{"12345", strings.Repeat("x", 129)} {
		rec := a.do(t, http.MethodPost, usersPath, jsonBody(t, map[string]any{"email": "len@example.com", "password": pw}), "Cookie", c)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("len %d: %d %s", len(pw), rec.Code, rec.Body)
		}
	}
}

func TestAdminResetPasswordSignsUserOut(t *testing.T) {
	a, c := adminApp(t)
	id := a.user(t, "target@example.com", "admin")
	targetCookie := a.mustLogin(t, "target@example.com", false)
	if rec := a.me(t, targetCookie); rec.Code != http.StatusOK {
		t.Fatalf("target before: %d %s", rec.Code, rec.Body)
	}
	if _, err := a.pool.Exec(t.Context(), `UPDATE users SET password_reset_token = 'abc', password_reset_expires = now() WHERE user_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	rec := a.do(t, http.MethodPost, usersPath+"/"+id.String()+"/reset-password", jsonBody(t, map[string]any{"password": "brand-new-pw"}), "Cookie", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	wantError(t, a.me(t, targetCookie), http.StatusUnauthorized, "session expired")
	if rec := a.login(t, "target@example.com", password, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("old password: %d", rec.Code)
	}
	if rec := a.login(t, "target@example.com", "brand-new-pw", false); rec.Code != http.StatusOK {
		t.Errorf("new password: %d %s", rec.Code, rec.Body)
	}
	var token *string
	if err := a.pool.QueryRow(t.Context(), `SELECT password_reset_token FROM users WHERE user_id = $1`, id).Scan(&token); err != nil || token != nil {
		t.Errorf("reset token %v (err %v), want cleared", token, err)
	}
	if rec := a.me(t, c); rec.Code != http.StatusOK {
		t.Errorf("admin session: %d", rec.Code)
	}
}

// An admin resetting their own password bumps their own session epoch too
// (PORT-NOTES 22): the reset answers 200, then the old cookie is refused.
func TestAdminSelfResetSignsCallerOut(t *testing.T) {
	a := newApp(t)
	id := a.user(t, adminEmail, "admin")
	c := a.mustLogin(t, adminEmail, false)
	rec := a.do(t, http.MethodPost, usersPath+"/"+id.String()+"/reset-password", jsonBody(t, map[string]any{"password": "brand-new-pw"}), "Cookie", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	wantError(t, a.me(t, c), http.StatusUnauthorized, "session expired")
	if rec := a.login(t, adminEmail, "brand-new-pw", false); rec.Code != http.StatusOK {
		t.Errorf("new password: %d %s", rec.Code, rec.Body)
	}
}

func TestAdminResetPasswordUnknownIs404(t *testing.T) {
	a, c := adminApp(t)
	rec := a.do(t, http.MethodPost, usersPath+"/00000000-0000-4000-8000-000000000000/reset-password",
		jsonBody(t, map[string]any{"password": "brand-new-pw"}), "Cookie", c)
	wantError(t, rec, http.StatusNotFound, "user not found")
}
