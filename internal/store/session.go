package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/rdb"
)

const sessionKeyPrefix = "px0:session:"

func sessionKey(token string) string { return sessionKeyPrefix + token }

// CreateSession persists the session in Postgres and writes it to the Redis
// cache so the first auth check after login is a cache hit.
func CreateSession(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) (*model.Session, error) {
	s := &model.Session{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, token, expires_at)
		 VALUES ($1, $2, $3)
		 RETURNING id, user_id, token, expires_at, created_at`,
		userID, token, expiresAt,
	).Scan(&s.ID, &s.UserID, &s.Token, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	cacheSession(ctx, s)
	return s, nil
}

// GetSessionByToken checks Redis first. On a miss it falls back to Postgres
// and back-fills the cache. Redis errors are treated as misses so the service
// continues to work when Redis is unavailable.
func GetSessionByToken(ctx context.Context, token string) (*model.Session, error) {
	if s := sessionFromCache(ctx, token); s != nil {
		return s, nil
	}

	s := &model.Session{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, user_id, token, expires_at, created_at
		 FROM sessions WHERE token = $1 AND expires_at > NOW()`,
		token,
	).Scan(&s.ID, &s.UserID, &s.Token, &s.ExpiresAt, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}

	cacheSession(ctx, s)
	return s, nil
}

// DeleteSession removes the session from Postgres and evicts it from the cache
// so a logged-out token cannot be replayed via a stale cache entry.
func DeleteSession(ctx context.Context, token string) error {
	_, err := db.Pool.Exec(ctx, "DELETE FROM sessions WHERE token = $1", token)
	if err != nil {
		return err
	}
	evictSession(ctx, token)
	return nil
}

// DeleteSessionsByUserID removes all sessions for a user from Postgres and evicts them from the cache.
func DeleteSessionsByUserID(ctx context.Context, userID uuid.UUID) error {
	rows, err := db.Pool.Query(ctx, "SELECT token FROM sessions WHERE user_id = $1", userID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return err
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Pool.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID)
	if err != nil {
		return err
	}

	for _, t := range tokens {
		evictSession(ctx, t)
	}

	return nil
}

// sessionFromCache returns a session from Redis or nil on any miss/error.
func sessionFromCache(ctx context.Context, token string) *model.Session {
	if rdb.Client == nil {
		return nil
	}
	data, err := rdb.Client.Get(ctx, sessionKey(token)).Bytes()
	if errors.Is(err, goredis.Nil) || err != nil {
		return nil
	}
	var s model.Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	if time.Now().After(s.ExpiresAt) {
		evictSession(ctx, token)
		return nil
	}
	return &s
}

func cacheSession(ctx context.Context, s *model.Session) {
	if rdb.Client == nil {
		return
	}
	ttl := time.Until(s.ExpiresAt)
	if ttl <= 0 {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	rdb.Client.Set(ctx, sessionKey(s.Token), data, ttl) //nolint:errcheck
}

func evictSession(ctx context.Context, token string) {
	if rdb.Client == nil {
		return
	}
	rdb.Client.Del(ctx, sessionKey(token)) //nolint:errcheck
}
