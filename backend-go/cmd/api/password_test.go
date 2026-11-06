package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPasswordInputValidation(t *testing.T) {
	for _, tc := range []struct {
		name, password, confirmation string
		ok                           bool
	}{
		{"valid", "secret1", "secret1", true},
		{"short", "12345", "12345", false},
		{"long", strings.Repeat("a", 129), strings.Repeat("a", 129), false},
		{"mismatch", "secret1", "secret2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePassword(tc.password, tc.confirmation)
			if (err == nil) != tc.ok {
				t.Fatalf("validation error: %v", err)
			}
		})
	}
}

func TestCommandRejectsExtraArgumentsAndNonterminal(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"set-password", "a@example.com", "secret1"}, "usage:"},
		{[]string{"set-password", "a@example.com"}, "interactive terminal"},
	} {
		var out bytes.Buffer
		called := false
		err := runPasswordCommand(tc.args, &out, false, func() (string, error) { called = true; return "secret1", nil })
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("expected %s, got %v", tc.want, err)
		}
		if called {
			t.Fatal("read password before checking command and terminal")
		}
		if strings.Contains(err.Error()+out.String(), "secret1") {
			t.Fatal("secret leaked")
		}
	}
}

func TestPasswordErrorsDoNotRevealSecrets(t *testing.T) {
	for _, passwords := range [][]string{{"secret1", "different"}, {"short", "short"}} {
		var out bytes.Buffer
		i := 0
		err := runPasswordCommand([]string{"set-password", "a@example.com"}, &out, true, func() (string, error) { p := passwords[i]; i++; return p, nil })
		if err == nil {
			t.Fatal("invalid password accepted")
		}
		for _, p := range passwords {
			if strings.Contains(out.String()+err.Error(), p) {
				t.Fatal("password leaked")
			}
		}
	}
}

func TestPasswordCommandRedactsDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://secretuser:secretpass@127.0.0.1:1/db")
	var out bytes.Buffer
	err := runPasswordCommand([]string{"set-password", "a@example.com"}, &out, true, func() (string, error) { return "secret1", nil })
	if err == nil {
		t.Fatal("expected connection failure")
	}
	for _, secret := range []string{"secretuser", "secretpass", "secret1"} {
		if strings.Contains(out.String()+err.Error(), secret) {
			t.Fatal("sensitive value leaked")
		}
	}
}
