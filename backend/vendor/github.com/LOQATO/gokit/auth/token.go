package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Tokens signs and reads HS256 access tokens. Leeway absorbs clock skew
// between machines (Docker Desktop's VM steps its clock back on resync; a
// zero-tolerance check on iat once signed people out at random).
type Tokens struct {
	Secret    []byte
	AccessTTL time.Duration
	Leeway    time.Duration
}

// Issue signs claims with iat (now) and exp (now + AccessTTL) added.
func (t Tokens) Issue(claims jwt.MapClaims) (string, error) {
	now := time.Now()
	out := jwt.MapClaims{}
	for k, v := range claims {
		out[k] = v
	}
	out["iat"] = now.Unix()
	out["exp"] = now.Add(t.AccessTTL).Unix()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, out).SignedString(t.Secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign: %w", err)
	}
	return s, nil
}

// ErrInvalidToken is any token Parse refuses.
var ErrInvalidToken = errors.New("auth: invalid token")

// Parse checks the signature (HS256 only), exp and iat (both within Leeway)
// and returns the claims.
func (t Tokens) Parse(token string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return t.Secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithLeeway(t.Leeway),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	return claims, nil
}
