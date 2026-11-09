package auth

import (
	"fmt"

	"github.com/alexedwards/argon2id"
)

// HashParams are argon2id's cost settings.
type HashParams struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLen     uint32
	KeyLen      uint32
}

// DefaultHashParams are argon2-cffi's PasswordHasher defaults, which TAGARA's
// Python used: hashes stay comparable in cost across the switch. Tests pass
// cheap params; production keeps these.
var DefaultHashParams = HashParams{Memory: 65536, Iterations: 3, Parallelism: 4, SaltLen: 16, KeyLen: 32}

// Hash returns a standard "$argon2id$v=19$m=...,t=...,p=...$salt$key" string.
func Hash(password string, p HashParams) (string, error) {
	encoded, err := argon2id.CreateHash(password, &argon2id.Params{
		Memory: p.Memory, Iterations: p.Iterations, Parallelism: p.Parallelism,
		SaltLength: p.SaltLen, KeyLength: p.KeyLen,
	})
	if err != nil {
		return "", fmt.Errorf("auth: hash: %w", err)
	}
	return encoded, nil
}

// Verify reports whether password matches encoded, in constant time. It reads
// any standard argon2id string, including argon2-cffi's. An error means the
// string is not one; a wrong password is (false, nil).
func Verify(password, encoded string) (bool, error) {
	ok, err := argon2id.ComparePasswordAndHash(password, encoded)
	if err != nil {
		return false, fmt.Errorf("auth: verify: %w", err)
	}
	return ok, nil
}
