package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

func SetTag(ctx context.Context, promptID uuid.UUID, version int, tag string) error {
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO prompt_tags (prompt_id, tag, version)
		VALUES ($1, $2, $3)
		ON CONFLICT (prompt_id, tag)
		DO UPDATE SET version = EXCLUDED.version, updated_at = NOW()
	`, promptID, tag, version)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrNotFound
		}
		return fmt.Errorf("set tag: %w", err)
	}
	return nil
}

func RemoveTag(ctx context.Context, promptID uuid.UUID, tag string) error {
	res, err := db.Pool.Exec(ctx, `
		DELETE FROM prompt_tags
		WHERE prompt_id = $1 AND tag = $2
	`, promptID, tag)
	if err != nil {
		return fmt.Errorf("remove tag: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func GetTagsForVersion(ctx context.Context, promptID uuid.UUID, version int) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT tag
		FROM prompt_tags
		WHERE prompt_id = $1 AND version = $2
		ORDER BY tag
	`, promptID, version)
	if err != nil {
		return nil, fmt.Errorf("get tags for version: %w", err)
	}
	defer rows.Close()

	tags := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func GetTagsForPrompt(ctx context.Context, promptID uuid.UUID) (map[int][]string, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT version, tag
		FROM prompt_tags
		WHERE prompt_id = $1
		ORDER BY version, tag
	`, promptID)
	if err != nil {
		return nil, fmt.Errorf("get tags for prompt: %w", err)
	}
	defer rows.Close()

	tagMap := make(map[int][]string)
	for rows.Next() {
		var version int
		var tag string
		if err := rows.Scan(&version, &tag); err != nil {
			return nil, err
		}
		tagMap[version] = append(tagMap[version], tag)
	}
	return tagMap, rows.Err()
}

func GetVersionByTag(ctx context.Context, promptID uuid.UUID, tag string) (*model.PromptVersion, error) {
	v := &model.PromptVersion{}
	err := db.Pool.QueryRow(ctx, `
		SELECT pv.id, pv.prompt_id, pv.version, pv.template, pv.status, pv.model, pv.model_params, pv.created_at, pv.published_at
		FROM prompt_versions pv
		JOIN prompt_tags pt ON pv.prompt_id = pt.prompt_id AND pv.version = pt.version
		WHERE pt.prompt_id = $1 AND pt.tag = $2
	`, promptID, tag).Scan(
		&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.Model, &v.ModelParams, &v.CreatedAt, &v.PublishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get version by tag: %w", err)
	}
	if err := populateTags(ctx, v); err != nil {
		return nil, fmt.Errorf("populate tags: %w", err)
	}
	return v, nil
}
