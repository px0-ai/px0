package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	v.Tags = []string{}
	return v, nil
}

func DuplicateVersion(ctx context.Context, promptID uuid.UUID, versionNum int) (*model.PromptVersion, error) {
	v := &model.PromptVersion{}
	err := db.Pool.QueryRow(ctx, `
		WITH source_version AS (
			SELECT template
			FROM prompt_versions
			WHERE prompt_id = $1 AND version = $2
		),
		next_version AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS v
			FROM prompt_versions
			WHERE prompt_id = $1
		)
		INSERT INTO prompt_versions (prompt_id, version, template, status)
		SELECT $1, nv.v, sv.template, 'draft'
		FROM source_version sv, next_version nv
		RETURNING id, prompt_id, version, template, status, created_at, published_at
	`, promptID, versionNum).Scan(
		&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("duplicate version: %w", err)
	}
	v.Tags = []string{}
	return v, nil
}

type VersionFilter struct {
	Status *string
	Tags   []string
}

func ListVersions(ctx context.Context, promptID uuid.UUID, filter VersionFilter) ([]*model.PromptVersion, error) {
	query := `SELECT DISTINCT pv.id, pv.prompt_id, pv.version, pv.template, pv.status, pv.created_at, pv.published_at
		 FROM prompt_versions pv`
	args := []any{promptID}
	joins := ""
	whereClauses := []string{"pv.prompt_id = $1"}

	if len(filter.Tags) > 0 {
		joins += " INNER JOIN prompt_tags pt ON pt.prompt_id = pv.prompt_id AND pt.version = pv.version"
		args = append(args, filter.Tags)
		whereClauses = append(whereClauses, fmt.Sprintf("pt.tag = ANY($%d)", len(args)))
	}

	if filter.Status != nil {
		args = append(args, *filter.Status)
		whereClauses = append(whereClauses, fmt.Sprintf("pv.status = $%d", len(args)))
	}

	query += joins
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += " ORDER BY pv.version DESC"

	rows, err := db.Pool.Query(ctx, query, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := populateTagsForList(ctx, promptID, versions); err != nil {
		return nil, fmt.Errorf("populate tags for list: %w", err)
	}

	return versions, nil
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
	if err := populateTags(ctx, v); err != nil {
		return nil, fmt.Errorf("populate tags: %w", err)
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
	if err := populateTags(ctx, v); err != nil {
		return nil, fmt.Errorf("populate tags: %w", err)
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
	if err := populateTags(ctx, v); err != nil {
		return nil, fmt.Errorf("populate tags: %w", err)
	}
	return v, nil
}

func PromoteVersion(ctx context.Context, promptID uuid.UUID, versionNum int) (*model.PromptVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

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

	var nextStatus string
	if currentStatus == model.VersionStatusDraft {
		nextStatus = model.VersionStatusStable
	} else if currentStatus == model.VersionStatusStable {
		nextStatus = model.VersionStatusLive
	} else {
		return nil, fmt.Errorf("cannot promote version in %s status: %w", currentStatus, ErrConflict)
	}

	// If promoting to live, we must demote the previous live version to stable.
	if nextStatus == model.VersionStatusLive {
		_, err = tx.Exec(ctx,
			`UPDATE prompt_versions SET status = 'stable'
			 WHERE prompt_id = $1 AND status = 'live'`,
			promptID,
		)
		if err != nil {
			return nil, fmt.Errorf("demote existing live version: %w", err)
		}
	}

	now := time.Now()
	v := &model.PromptVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE prompt_versions
		 SET status = $1, published_at = $2
		 WHERE prompt_id = $3 AND version = $4
		 RETURNING id, prompt_id, version, template, status, created_at, published_at`,
		nextStatus, now, promptID, versionNum,
	).Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt)
	if err != nil {
		return nil, fmt.Errorf("promote version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	if err := populateTags(ctx, v); err != nil {
		return nil, fmt.Errorf("populate tags: %w", err)
	}
	return v, nil
}

func DemoteVersion(ctx context.Context, promptID uuid.UUID, versionNum int) (*model.PromptVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

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

	if currentStatus != model.VersionStatusLive {
		return nil, fmt.Errorf("only live versions can be demoted: %w", ErrConflict)
	}

	v := &model.PromptVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE prompt_versions
		 SET status = 'stable'
		 WHERE prompt_id = $1 AND version = $2
		 RETURNING id, prompt_id, version, template, status, created_at, published_at`,
		promptID, versionNum,
	).Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt)
	if err != nil {
		return nil, fmt.Errorf("demote version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	if err := populateTags(ctx, v); err != nil {
		return nil, fmt.Errorf("populate tags: %w", err)
	}
	return v, nil
}

func ArchiveVersion(ctx context.Context, promptID uuid.UUID, versionNum int) (*model.PromptVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

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

	if currentStatus == model.VersionStatusArchived {
		return nil, fmt.Errorf("version is already archived: %w", ErrConflict)
	}

	v := &model.PromptVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE prompt_versions
		 SET status = 'archived'
		 WHERE prompt_id = $1 AND version = $2
		 RETURNING id, prompt_id, version, template, status, created_at, published_at`,
		promptID, versionNum,
	).Scan(&v.ID, &v.PromptID, &v.Version, &v.Template, &v.Status, &v.CreatedAt, &v.PublishedAt)
	if err != nil {
		return nil, fmt.Errorf("archive version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	if err := populateTags(ctx, v); err != nil {
		return nil, fmt.Errorf("populate tags: %w", err)
	}
	return v, nil
}

func DeleteVersion(ctx context.Context, promptID uuid.UUID, versionNum int) error {
	var status string
	err := db.Pool.QueryRow(ctx,
		`SELECT status FROM prompt_versions WHERE prompt_id = $1 AND version = $2`,
		promptID, versionNum,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("check version status: %w", err)
	}

	if status == model.VersionStatusDraft {
		_, err = db.Pool.Exec(ctx,
			`DELETE FROM prompt_versions WHERE prompt_id = $1 AND version = $2`,
			promptID, versionNum,
		)
		if err != nil {
			return fmt.Errorf("delete draft version: %w", err)
		}
	} else {
		_, err = db.Pool.Exec(ctx,
			`UPDATE prompt_versions SET status = 'archived' WHERE prompt_id = $1 AND version = $2`,
			promptID, versionNum,
		)
		if err != nil {
			return fmt.Errorf("archive version on delete: %w", err)
		}
	}
	return nil
}

func populateTags(ctx context.Context, v *model.PromptVersion) error {
	if v == nil {
		return nil
	}
	tags, err := GetTagsForVersion(ctx, v.PromptID, v.Version)
	if err != nil {
		return err
	}
	v.Tags = tags
	return nil
}

func populateTagsForList(ctx context.Context, promptID uuid.UUID, versions []*model.PromptVersion) error {
	if len(versions) == 0 {
		return nil
	}
	tagMap, err := GetTagsForPrompt(ctx, promptID)
	if err != nil {
		return err
	}
	for _, v := range versions {
		if tags, ok := tagMap[v.Version]; ok {
			v.Tags = tags
		} else {
			v.Tags = []string{}
		}
	}
	return nil
}
