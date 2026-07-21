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

func CreateAPIKey(ctx context.Context, name string, orgID uuid.UUID, teamIDs []uuid.UUID, operation string, keyHash string) (*model.APIKey, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	k := &model.APIKey{}
	// Insert API Key. Note: we set team_id to NULL, but can also set it to the first team if any team is provided
	var firstTeamID *uuid.UUID
	if len(teamIDs) > 0 {
		firstTeamID = &teamIDs[0]
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO api_keys (name, org_id, team_id, operation, key_hash)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, name, org_id, team_id, operation, key_hash, created_at, last_used_at`,
		name, orgID, firstTeamID, operation, keyHash,
	).Scan(&k.ID, &k.Name, &k.OrgID, &k.TeamID, &k.Operation, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create api key: %w", err)
	}

	for _, teamID := range teamIDs {
		_, err = tx.Exec(ctx,
			`INSERT INTO api_key_teams (api_key_id, team_id) VALUES ($1, $2)`,
			k.ID, teamID,
		)
		if err != nil {
			return nil, fmt.Errorf("create api key team mapping: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return k, nil
}

func ListAPIKeysForOrg(ctx context.Context, orgID uuid.UUID) ([]*model.APIKey, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, name, org_id, team_id, operation, key_hash, created_at, last_used_at
		 FROM api_keys WHERE org_id = $1 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []*model.APIKey
	for rows.Next() {
		k := &model.APIKey{}
		if err := rows.Scan(&k.ID, &k.Name, &k.OrgID, &k.TeamID, &k.Operation, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func GetAPIKeyByHash(ctx context.Context, keyHash string) (*model.APIKey, error) {
	k := &model.APIKey{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, name, org_id, team_id, operation, key_hash, created_at, last_used_at
		 FROM api_keys WHERE key_hash = $1`,
		keyHash,
	).Scan(&k.ID, &k.Name, &k.OrgID, &k.TeamID, &k.Operation, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get api key: %w", err)
	}
	return k, nil
}

func GetAPIKeyTeams(ctx context.Context, apiKeyID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT team_id FROM api_key_teams WHERE api_key_id = $1`,
		apiKeyID,
	)
	if err != nil {
		return nil, fmt.Errorf("get api key teams: %w", err)
	}
	defer rows.Close()

	var teamIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		teamIDs = append(teamIDs, id)
	}
	return teamIDs, rows.Err()
}

func GetAPIKeyByID(ctx context.Context, id uuid.UUID) (*model.APIKey, error) {
	k := &model.APIKey{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, name, org_id, team_id, operation, key_hash, created_at, last_used_at
		 FROM api_keys WHERE id = $1`,
		id,
	).Scan(&k.ID, &k.Name, &k.OrgID, &k.TeamID, &k.Operation, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get api key by id: %w", err)
	}
	return k, nil
}

func UpdateAPIKey(ctx context.Context, id uuid.UUID, name string, teamIDs []uuid.UUID, operation string) (*model.APIKey, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var firstTeamID *uuid.UUID
	if len(teamIDs) > 0 {
		firstTeamID = &teamIDs[0]
	}

	k := &model.APIKey{}
	err = tx.QueryRow(ctx,
		`UPDATE api_keys SET name = $1, team_id = $2, operation = $3 WHERE id = $4
		 RETURNING id, name, org_id, team_id, operation, key_hash, created_at, last_used_at`,
		name, firstTeamID, operation, id,
	).Scan(&k.ID, &k.Name, &k.OrgID, &k.TeamID, &k.Operation, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update api key: %w", err)
	}

	// Recreate team mappings
	_, err = tx.Exec(ctx, "DELETE FROM api_key_teams WHERE api_key_id = $1", id)
	if err != nil {
		return nil, fmt.Errorf("delete old api key teams: %w", err)
	}

	for _, teamID := range teamIDs {
		_, err = tx.Exec(ctx,
			`INSERT INTO api_key_teams (api_key_id, team_id) VALUES ($1, $2)`,
			k.ID, teamID,
		)
		if err != nil {
			return nil, fmt.Errorf("create api key team mapping: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
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

func RegenerateAPIKey(ctx context.Context, id uuid.UUID, newKeyHash string) (*model.APIKey, error) {
	k := &model.APIKey{}
	err := db.Pool.QueryRow(ctx,
		`UPDATE api_keys SET key_hash = $1 WHERE id = $2
		 RETURNING id, name, org_id, team_id, operation, key_hash, created_at, last_used_at`,
		newKeyHash, id,
	).Scan(&k.ID, &k.Name, &k.OrgID, &k.TeamID, &k.Operation, &k.KeyHash, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("regenerate api key: %w", err)
	}
	return k, nil
}
