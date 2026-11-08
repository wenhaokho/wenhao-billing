package auth

import (
	"os"
	"testing"

	gauth "github.com/LOQATO/gokit/auth"
)

// TestMain hashes at a token argon2id cost: production's 19 MiB, two passes
// per hash makes the package slow under -race. Verification reads the cost
// from each hash, so nothing else changes.
func TestMain(m *testing.M) {
	HashParams = gauth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	os.Exit(m.Run())
}
