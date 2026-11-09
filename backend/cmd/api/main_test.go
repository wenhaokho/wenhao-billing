package main

import (
	"os"
	"testing"

	gauth "github.com/LOQATO/gokit/auth"

	"github.com/wenhaokho/wenhao-billing/backend/internal/auth"
)

// TestMain hashes at a token argon2id cost: production's 19 MiB, two passes
// per hash makes the package slow under -race. Verification reads the cost
// from each hash, so nothing else changes.
func TestMain(m *testing.M) {
	auth.HashParams = gauth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	os.Exit(m.Run())
}
