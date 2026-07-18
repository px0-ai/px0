package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

func CreateTool(ctx context.Context, projectID uuid.UUID, slug, name, description string) (*model.Tool, error) {
	t := &model.Tool{}
	err := db.Pool.QueryRow(ctx, `
		INSERT INTO tools (project_id, slug, name, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, project_id, slug, name, description, created_at, updated_at
	`, projectID, slug, name, description).Scan(
		&t.ID, &t.ProjectID, &t.Slug, &t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "uq_") {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create tool: %w", err)
	}
	return t, nil
}

func ListTools(ctx context.Context, projectIDs []uuid.UUID) ([]*model.Tool, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, project_id, slug, name, description, created_at, updated_at
		FROM tools
		WHERE project_id = ANY($1)
		ORDER BY created_at DESC
	`, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	defer rows.Close()

	var tools []*model.Tool
	for rows.Next() {
		t := &model.Tool{}
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Slug, &t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

func GetToolByID(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) (*model.Tool, error) {
	t := &model.Tool{}
	err := db.Pool.QueryRow(ctx, `
		SELECT id, project_id, slug, name, description, created_at, updated_at
		FROM tools
		WHERE id = $1 AND project_id = ANY($2)
	`, id, projectIDs).Scan(
		&t.ID, &t.ProjectID, &t.Slug, &t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get tool by id: %w", err)
	}
	return t, nil
}

func GetToolBySlug(ctx context.Context, slug string, projectIDs []uuid.UUID) (*model.Tool, error) {
	t := &model.Tool{}
	err := db.Pool.QueryRow(ctx, `
		SELECT id, project_id, slug, name, description, created_at, updated_at
		FROM tools
		WHERE slug = $1 AND project_id = ANY($2)
	`, slug, projectIDs).Scan(
		&t.ID, &t.ProjectID, &t.Slug, &t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get tool by slug: %w", err)
	}
	return t, nil
}

func UpdateTool(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID, slug, name, description string) (*model.Tool, error) {
	t := &model.Tool{}
	err := db.Pool.QueryRow(ctx, `
		UPDATE tools
		SET slug = $1, name = $2, description = $3, updated_at = NOW()
		WHERE id = $4 AND project_id = ANY($5)
		RETURNING id, project_id, slug, name, description, created_at, updated_at
	`, slug, name, description, id, projectIDs).Scan(
		&t.ID, &t.ProjectID, &t.Slug, &t.Name, &t.Description, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "uq_") {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("update tool: %w", err)
	}
	return t, nil
}

func DeleteTool(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) error {
	res, err := db.Pool.Exec(ctx, `
		DELETE FROM tools
		WHERE id = $1 AND project_id = ANY($2)
	`, id, projectIDs)
	if err != nil {
		return fmt.Errorf("delete tool: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func CreateToolVersion(ctx context.Context, toolID uuid.UUID, inputSchema, outputSchema json.RawMessage) (*model.ToolVersion, error) {
	v := &model.ToolVersion{}
	err := db.Pool.QueryRow(ctx, `
		WITH next_version AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS v
			FROM tool_versions
			WHERE tool_id = $1
		)
		INSERT INTO tool_versions (tool_id, version, input_schema, output_schema, status)
		VALUES ($1, (SELECT v FROM next_version), $2, $3, 'draft')
		RETURNING id, tool_id, version, input_schema, output_schema, status, created_at, updated_at
	`, toolID, inputSchema, outputSchema).Scan(
		&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create tool version: %w", err)
	}
	return v, nil
}

func DuplicateToolVersion(ctx context.Context, toolID uuid.UUID, versionNum int) (*model.ToolVersion, error) {
	v := &model.ToolVersion{}
	err := db.Pool.QueryRow(ctx, `
		WITH source_version AS (
			SELECT input_schema, output_schema
			FROM tool_versions
			WHERE tool_id = $1 AND version = $2
		),
		next_version AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS v
			FROM tool_versions
			WHERE tool_id = $1
		)
		INSERT INTO tool_versions (tool_id, version, input_schema, output_schema, status)
		SELECT $1, nv.v, sv.input_schema, sv.output_schema, 'draft'
		FROM source_version sv, next_version nv
		RETURNING id, tool_id, version, input_schema, output_schema, status, created_at, updated_at
	`, toolID, versionNum).Scan(
		&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("duplicate tool version: %w", err)
	}
	return v, nil
}

func ListToolVersions(ctx context.Context, toolID uuid.UUID) ([]*model.ToolVersion, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, tool_id, version, input_schema, output_schema, status, created_at, updated_at
		FROM tool_versions
		WHERE tool_id = $1
		ORDER BY version DESC
	`, toolID)
	if err != nil {
		return nil, fmt.Errorf("list tool versions: %w", err)
	}
	defer rows.Close()

	var versions []*model.ToolVersion
	for rows.Next() {
		v := &model.ToolVersion{}
		if err := rows.Scan(&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func GetToolVersion(ctx context.Context, toolID uuid.UUID, versionNum int) (*model.ToolVersion, error) {
	v := &model.ToolVersion{}
	err := db.Pool.QueryRow(ctx, `
		SELECT id, tool_id, version, input_schema, output_schema, status, created_at, updated_at
		FROM tool_versions
		WHERE tool_id = $1 AND version = $2
	`, toolID, versionNum).Scan(
		&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get tool version: %w", err)
	}
	return v, nil
}

func GetLiveToolVersion(ctx context.Context, toolID uuid.UUID) (*model.ToolVersion, error) {
	v := &model.ToolVersion{}
	err := db.Pool.QueryRow(ctx, `
		SELECT id, tool_id, version, input_schema, output_schema, status, created_at, updated_at
		FROM tool_versions
		WHERE tool_id = $1 AND status = 'live'
	`, toolID).Scan(
		&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get live tool version: %w", err)
	}
	return v, nil
}

func UpdateToolVersion(ctx context.Context, id uuid.UUID, inputSchema, outputSchema json.RawMessage) (*model.ToolVersion, error) {
	v := &model.ToolVersion{}
	err := db.Pool.QueryRow(ctx, `
		UPDATE tool_versions
		SET input_schema = $1, output_schema = $2, updated_at = NOW()
		WHERE id = $3 AND status = 'draft'
		RETURNING id, tool_id, version, input_schema, output_schema, status, created_at, updated_at
	`, inputSchema, outputSchema, id).Scan(
		&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update tool version: %w", err)
	}
	return v, nil
}

func DeleteToolVersion(ctx context.Context, toolID uuid.UUID, versionNum int) error {
	var status string
	err := db.Pool.QueryRow(ctx,
		`SELECT status FROM tool_versions WHERE tool_id = $1 AND version = $2`,
		toolID, versionNum,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("check tool version status: %w", err)
	}

	if status == "draft" {
		_, err = db.Pool.Exec(ctx,
			`DELETE FROM tool_versions WHERE tool_id = $1 AND version = $2`,
			toolID, versionNum,
		)
		if err != nil {
			return fmt.Errorf("delete draft tool version: %w", err)
		}
	} else {
		_, err = db.Pool.Exec(ctx,
			`UPDATE tool_versions SET status = 'archived', updated_at = NOW() WHERE tool_id = $1 AND version = $2`,
			toolID, versionNum,
		)
		if err != nil {
			return fmt.Errorf("archive tool version on delete: %w", err)
		}
	}
	return nil
}

func PromoteToolVersion(ctx context.Context, toolID uuid.UUID, versionNum int) (*model.ToolVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM tool_versions
		 WHERE tool_id = $1 AND version = $2
		 FOR UPDATE`,
		toolID, versionNum,
	).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock tool version: %w", err)
	}

	var nextStatus string
	if currentStatus == "draft" {
		nextStatus = "stable"
	} else if currentStatus == "stable" {
		nextStatus = "live"
	} else {
		return nil, fmt.Errorf("cannot promote tool version in %s status: %w", currentStatus, ErrConflict)
	}

	// If promoting to live, we must demote the previous live version to stable.
	if nextStatus == "live" {
		_, err = tx.Exec(ctx,
			`UPDATE tool_versions SET status = 'stable', updated_at = NOW()
			 WHERE tool_id = $1 AND status = 'live'`,
			toolID,
		)
		if err != nil {
			return nil, fmt.Errorf("demote existing live tool version: %w", err)
		}
	}

	v := &model.ToolVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE tool_versions
		 SET status = $1, updated_at = NOW()
		 WHERE tool_id = $2 AND version = $3
		 RETURNING id, tool_id, version, input_schema, output_schema, status, created_at, updated_at`,
		nextStatus, toolID, versionNum,
	).Scan(&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("promote tool version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return v, nil
}

func DemoteToolVersion(ctx context.Context, toolID uuid.UUID, versionNum int) (*model.ToolVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM tool_versions
		 WHERE tool_id = $1 AND version = $2
		 FOR UPDATE`,
		toolID, versionNum,
	).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock tool version: %w", err)
	}

	if currentStatus != "live" {
		return nil, fmt.Errorf("only live tool versions can be demoted: %w", ErrConflict)
	}

	v := &model.ToolVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE tool_versions
		 SET status = 'stable', updated_at = NOW()
		 WHERE tool_id = $1 AND version = $2
		 RETURNING id, tool_id, version, input_schema, output_schema, status, created_at, updated_at`,
		toolID, versionNum,
	).Scan(&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("demote tool version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return v, nil
}

func ArchiveToolVersion(ctx context.Context, toolID uuid.UUID, versionNum int) (*model.ToolVersion, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM tool_versions
		 WHERE tool_id = $1 AND version = $2
		 FOR UPDATE`,
		toolID, versionNum,
	).Scan(&currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock tool version: %w", err)
	}

	if currentStatus == "archived" {
		return nil, fmt.Errorf("tool version is already archived: %w", ErrConflict)
	}

	v := &model.ToolVersion{}
	err = tx.QueryRow(ctx,
		`UPDATE tool_versions
		 SET status = 'archived', updated_at = NOW()
		 WHERE tool_id = $1 AND version = $2
		 RETURNING id, tool_id, version, input_schema, output_schema, status, created_at, updated_at`,
		toolID, versionNum,
	).Scan(&v.ID, &v.ToolID, &v.Version, &v.InputSchema, &v.OutputSchema, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("archive tool version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return v, nil
}
