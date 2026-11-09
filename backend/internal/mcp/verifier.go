// Package mcp holds the bearer verifier for the MCP transport.
// Python's BillingTokenVerifier validates the access token's signature and
// expiry, then loads the user fresh on every request and requires role admin,
// so a deleted or demoted user loses access on the next request without
// waiting for the token TTL. The verifier here does the same: oauth.Tokens
// verifies the JWT, then a fresh SELECT re-checks the admin.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// verifier is the SDK's TokenVerifier backed by the project's own tokens.
type verifier struct {
	pool   *pgxpool.Pool
	clock  platform.Clock
	tokens *oauth.Tokens
}

func newVerifier(pool *pgxpool.Pool, clock platform.Clock, tokens *oauth.Tokens) *verifier {
	return &verifier{pool: pool, clock: clock, tokens: tokens}
}

func (v *verifier) verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	userID, err := v.tokens.VerifyAccess(token, v.clock.Now())
	if err != nil {
		return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, err.Error())
	}
	// Re-check admin on every request: a valid signature is not enough.
	var role string
	if err := v.pool.QueryRow(ctx, `SELECT role FROM users WHERE user_id = $1`, userID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: not an admin user", auth.ErrInvalidToken)
		}
		// An unexpected database error is a 500, not a 401.
		return nil, fmt.Errorf("mcp verifier: %w", err)
	}
	if role != "admin" {
		return nil, fmt.Errorf("%w: not an admin user", auth.ErrInvalidToken)
	}
	return &auth.TokenInfo{
		Scopes:     []string{},
		Expiration: time.Time{},
		UserID:     userID.String(),
		Extra:      map[string]any{"user_id": userID.String()},
	}, nil
}

// principal returns the authenticated admin from the request context, for
// tool handlers. It is the TokenInfo the verifier put on the context.
func principal(ctx context.Context) (uuid.UUID, bool) {
	ti := auth.TokenInfoFromContext(ctx)
	if ti == nil || ti.UserID == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(ti.UserID)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}
