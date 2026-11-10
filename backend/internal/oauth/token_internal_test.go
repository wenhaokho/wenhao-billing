package oauth

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The spent-code id is the same for every base64url spelling of a code's
// signature, so single use holds even if a respelling ever verified.
func TestCodeIDIgnoresRespelling(t *testing.T) {
	t.Parallel()
	code := NewTokens("s", time.Hour, time.Hour).MintCode(CodeClaims{UserID: uuid.New(), ClientID: "c"}, time.Now())
	want, err := codeID(code)
	if err != nil {
		t.Fatal(err)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, code[len(code)-1])
	for low := range 4 {
		alt := code[:len(code)-1] + string(alphabet[i&^3|low])
		got, err := codeID(alt)
		if err != nil || got != want {
			t.Fatalf("codeID(%q) = %q, %v; want %q", alt[len(alt)-4:], got, err, want)
		}
	}
	other := NewTokens("s", time.Hour, time.Hour).MintCode(CodeClaims{UserID: uuid.New(), ClientID: "c"}, time.Now())
	if got, _ := codeID(other); got == want {
		t.Fatal("two codes share an id")
	}
}
