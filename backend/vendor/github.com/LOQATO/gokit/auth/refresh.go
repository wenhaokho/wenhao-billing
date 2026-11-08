package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RefreshStore keeps opaque refresh tokens in Redis. Each is single use:
// Rotate spends it and issues the next. Presenting a spent token again means
// it was stolen, so every token of that subject is revoked.
//
// Keys: <Prefix>:t:<token> -> subject (TTL), <Prefix>:spent:<token> ->
// subject (TTL), <Prefix>:s:<subject> -> set of live tokens.
type RefreshStore struct {
	Redis  *redis.Client
	Prefix string
	TTL    time.Duration
}

// ErrRefreshInvalid is a missing, expired, revoked or reused token.
var ErrRefreshInvalid = errors.New("auth: refresh token invalid")

// Issue creates a token for subject.
func (s RefreshStore) Issue(ctx context.Context, subject string) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	pipe := s.Redis.TxPipeline()
	pipe.Set(ctx, s.key("t", token), subject, s.TTL)
	pipe.SAdd(ctx, s.key("s", subject), token)
	pipe.Expire(ctx, s.key("s", subject), s.TTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("auth: refresh: %w", err)
	}
	return token, nil
}

// KEYS: 1 the token, 2 its spent marker, 3 the next token, 4 the
// subject set's key prefix (completed with the subject inside the script).
// ARGV: 1 TTL in seconds, 2 the next token's id, 3 the token's own id.
// Spending, marking, issuing the next token and, for a reused token,
// revoking the subject all happen in one step: no sign-in or revocation can
// land in between.
var rotate = redis.NewScript(`
local subject = redis.call('GET', KEYS[1])
if subject then
  local set = KEYS[4] .. subject
  redis.call('DEL', KEYS[1])
  redis.call('SET', KEYS[2], subject, 'EX', ARGV[1])
  redis.call('SREM', set, ARGV[3])
  redis.call('SET', KEYS[3], subject, 'EX', ARGV[1])
  redis.call('SADD', set, ARGV[2])
  redis.call('EXPIRE', set, ARGV[1])
  return {'ok', subject}
end
local spent = redis.call('GET', KEYS[2])
if spent then
  local set = KEYS[4] .. spent
  local tprefix = string.sub(KEYS[1], 1, string.len(KEYS[1]) - string.len(ARGV[3]))
  for _, t in ipairs(redis.call('SMEMBERS', set)) do
    redis.call('DEL', tprefix .. t)
  end
  redis.call('DEL', set)
  return {'reused', spent}
end
return {'missing', ''}
`)

// Rotate spends token and returns its subject and the next token. A token
// presented again after it was spent was stolen: every token of its subject
// is revoked.
func (s RefreshStore) Rotate(ctx context.Context, token string) (subject, next string, err error) {
	next, err = newToken()
	if err != nil {
		return "", "", err
	}
	keys := []string{s.key("t", token), s.key("spent", token), s.key("t", next), s.Prefix + ":s:"}
	res, err := rotate.Run(ctx, s.Redis, keys, int(s.TTL.Seconds()), next, token).StringSlice()
	if err != nil {
		return "", "", fmt.Errorf("auth: refresh: %w", err)
	}
	if res[0] == "ok" {
		return res[1], next, nil
	}
	return "", "", ErrRefreshInvalid
}

// KEYS: 1 the subject's set, 2 the token key prefix. One script, so a token
// issued while revoking cannot survive it (the session a password change is
// meant to end).
var revokeAll = redis.NewScript(`
for _, t in ipairs(redis.call('SMEMBERS', KEYS[1])) do
  redis.call('DEL', KEYS[2] .. t)
end
redis.call('DEL', KEYS[1])
return 1
`)

// RevokeAll ends every refresh token of subject.
func (s RefreshStore) RevokeAll(ctx context.Context, subject string) error {
	if err := revokeAll.Run(ctx, s.Redis, []string{s.key("s", subject), s.Prefix + ":t:"}).Err(); err != nil {
		return fmt.Errorf("auth: refresh: %w", err)
	}
	return nil
}

func (s RefreshStore) key(kind, id string) string { return s.Prefix + ":" + kind + ":" + id }

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: refresh: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
