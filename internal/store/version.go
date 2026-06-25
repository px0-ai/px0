package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

func CreateVersion(ctx context.Context, promptID uuid.UUID, template string) (*model.PromptVersion, error) {
	v := &model.PromptVersion{}
	err := db.Pool.QueryRow(ctx, `
		WITH next_version AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS v
			FROM prompt_versions
			WHERE prompt_id = $1
		)
		INSERT INTO prompt_versions (prompt_id, version, template, status)
		SELECT $1, v, $2, 'draft'
		FROM next_version
		RETURNING id, prompt_id, version, template, status, created_at, published_at
	`, promptID, template).Scan(
		&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create version: %w", err)
	}
	return v, nil
}

func ListVersions(ctx context.Context, promptID uuid.UUID) ([]*model.PromptVersion, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, prompt_id, version, template, status, created_at, published_at
		 FROM prompt_versions
		 WHERE prompt_id = $1
		 ORDER BY version DESC`,
		promptID,
	)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	defer rows.Close()

	var versions []*model.PromptVersion
	for rows.Next() {
		v := &model.PromptVersion{}
		if err := rows.Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func GetVersion(ctx context.Context, promptID uuid.UUID, versionNum int) (*model.PromptVersion, error) {
	v := &model.PromptVersion{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, prompt_id, version, template, status, created_at, published_at
		 FROM prompt_versions
		 WHERE prompt_id = $1 AND version = $2`,
		promptID, versionNum,
	).Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get version: %w", err)
	}
	return v, nil
}

func GetLiveVersion(ctx context.Context, promptID uuid.UUID) (*model.PromptVersion, error) {
	v := &model.PromptVersion{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, prompt_id, version, template, status, created_at, published_at
		 FROM prompt_versions
		 WHERE prompt_id = $1 AND status = 'live'`,
		promptID,
	).Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get live version: %w", err)
	}
	return v, nil
}

func UpdateVersionTemplate(ctx context.Context, id uuid.UUID, template string) (*model.PromptVersion, error) {
	v := &model.PromptVersion{}
	err := db.Pool.QueryRow(ctx,
		`UPDATE prompt_versions
		 SET template = $1
		 WHERE id = $2 AND status = 'draft'
		 RETURNING id, prompt_id, version, template, status, created_at, published_at`,
		template, id,
	).Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update version: %w", err)
	}
	return v, nil
}

func PublishVersion(ctx context.Context, promptID uuid.UUID, versionNum int) (*model.PromptVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the target version and check it exists and is a draft.
	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM prompt_versions
		 WHERE prompt_id = $1 AND version = $2
		 FOR UPDATE`,
		promptID, versionNum,
	).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock version: %w", err)
	}
	if currentStatus != model.VersionStatusDraft {
		return nil, fmt.Errorf("version is already %s: %w", currentStatus, ErrConflict)
	}

	// Archive the current live version if any.
	_, err = tx.Exec(ctx,
		`UPDATE prompt_versions SET status = 'archived'
		 WHERE prompt_id = $1 AND status = 'live'`,
		promptID,
	)
	if err != nil {
		return nil, fmt.Errorf("archive live version: %w", err)
	}

	// Publish the target version.
	now := time.Now()
	v := &model.PromptVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE prompt_versions
		 SET status = 'live', published_at = $1
		 WHERE prompt_id = $2 AND version = $3
		 RETURNING id, prompt_id, version, template, status, created_at, published_at`,
		now, promptID, versionNum,
	).Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt)
	if err != nil {
		return nil, fmt.Errorf("publish version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return v, nil
}
