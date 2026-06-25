package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

func CreateAPIKey(ctx context.Context, name string, teamID uuid.UUID, keyPrefix, keyHash string) (*model.APIKey, error) {
	k := &model.APIKey{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO api_keys (name, team_id, key_prefix, key_hash)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, name, team_id, key_prefix, key_hash, created_at, last_used_at`,
		name, teamID, keyPrefix, keyHash,
	).Scan(&k.ID, &k.Name, &k.TeamID, &k.KeyPrefix, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create api key: %w", err)
	}
	return k, nil
}

func ListAPIKeys(ctx context.Context, teamIDs []uuid.UUID) ([]*model.APIKey, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, name, team_id, key_prefix, key_hash, created_at, last_used_at
		 FROM api_keys WHERE team_id = ANY($1) ORDER BY created_at DESC`,
		teamIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []*model.APIKey
	for rows.Next() {
		k := &model.APIKey{}
		if err := rows.Scan(&k.ID, &k.Name, &k.TeamID, &k.KeyPrefix, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func GetAPIKeyByHash(ctx context.Context, keyHash string) (*model.APIKey, error) {
	k := &model.APIKey{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, name, team_id, key_prefix, key_hash, created_at, last_used_at
		 FROM api_keys WHERE key_hash = $1`,
		keyHash,
	).Scan(&k.ID, &k.Name, &k.TeamID, &k.KeyPrefix, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get api key: %w", err)
	}
	return k, nil
}

func DeleteAPIKey(ctx context.Context, id uuid.UUID) error {
	result, err := db.Pool.Exec(ctx, "DELETE FROM api_keys WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func TouchAPIKey(ctx context.Context, id uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		"UPDATE api_keys SET last_used_at = $1 WHERE id = $2",
		time.Now(), id,
	)
	return err
}
