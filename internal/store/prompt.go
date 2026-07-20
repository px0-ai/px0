package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

type PromptFilter struct {
	ProjectIDs []uuid.UUID
	Tags       []string
	Archived   *bool
	Status     *string
}

func CreatePrompt(ctx context.Context, projectID uuid.UUID, slug, name, description string) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO prompts (project_id, slug, name, description, status)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, project_id, slug, name, description, status, schema, created_at, updated_at`,
		projectID, slug, name, description, model.PromptStatusActive,
	).Scan(&p.ID, &p.ProjectID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.Schema, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create prompt: %w", err)
	}
	return p, nil
}

func ListPrompts(ctx context.Context, filter PromptFilter) ([]*model.Prompt, error) {
	query := `SELECT DISTINCT p.id, p.project_id, p.slug, p.name, p.description, p.status, p.schema, p.created_at, p.updated_at
			  FROM prompts p`
	args := []any{}
	joins := ""
	whereClauses := []string{}

	if len(filter.Tags) > 0 {
		joins += " INNER JOIN prompt_tags pt ON pt.prompt_id = p.id"
		args = append(args, filter.Tags)
		whereClauses = append(whereClauses, fmt.Sprintf("pt.tag = ANY($%d)", len(args)))
	}

	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		whereClauses = append(whereClauses, fmt.Sprintf("p.project_id = ANY($%d)", len(args)))
	}

	if filter.Status != nil {
		args = append(args, *filter.Status)
		whereClauses = append(whereClauses, fmt.Sprintf("p.status = $%d", len(args)))
	} else if filter.Archived != nil {
		statusVal := model.PromptStatusActive
		if *filter.Archived {
			statusVal = model.PromptStatusArchived
		}
		args = append(args, statusVal)
		whereClauses = append(whereClauses, fmt.Sprintf("p.status = $%d", len(args)))
	}

	query += joins
	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	query += " ORDER BY p.created_at DESC"

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

	var prompts []*model.Prompt
	for rows.Next() {
		p := &model.Prompt{}
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.Schema, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

func GetPromptByID(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, project_id, slug, name, description, status, schema, created_at, updated_at
		 FROM prompts
		 WHERE id = $1 AND project_id = ANY($2)`,
		id, projectIDs,
	).Scan(&p.ID, &p.ProjectID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.Schema, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

func GetPromptBySlug(ctx context.Context, slug string, projectIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, project_id, slug, name, description, status, schema, created_at, updated_at
		 FROM prompts
		 WHERE slug = $1 AND project_id = ANY($2)`,
		slug, projectIDs,
	).Scan(&p.ID, &p.ProjectID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.Schema, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt by slug: %w", err)
	}
	return p, nil
}

func ArchivePrompt(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) error {
	// First check if the prompt belongs to one of the provided projects
	_, err := GetPromptByID(ctx, id, projectIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	result, err := db.Pool.Exec(ctx, "UPDATE prompts SET status = 'archived' WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("archive prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func UpdatePrompt(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID, description string) (*model.Prompt, error) {
	// First check if the prompt belongs to one of the allowed projects
	_, err := GetPromptByID(ctx, id, projectIDs)
	if err != nil {
		return nil, err // ErrNotFound if not found or no access
	}

	p := &model.Prompt{}
	err = db.Pool.QueryRow(ctx,
		`UPDATE prompts SET description = $1, updated_at = NOW() WHERE id = $2
		 RETURNING id, project_id, slug, name, description, status, schema, created_at, updated_at`,
		description, id,
	).Scan(&p.ID, &p.ProjectID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.Schema, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update prompt: %w", err)
	}
	return p, nil
}

func UpdatePromptSchema(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID, schema map[string]any) (*model.Prompt, error) {
	// First check if the prompt belongs to one of the allowed projects
	_, err := GetPromptByID(ctx, id, projectIDs)
	if err != nil {
		return nil, err // ErrNotFound if not found or no access
	}

	p := &model.Prompt{}
	err = db.Pool.QueryRow(ctx,
		`UPDATE prompts SET schema = $1, updated_at = NOW() WHERE id = $2
		 RETURNING id, project_id, slug, name, description, status, schema, created_at, updated_at`,
		schema, id,
	).Scan(&p.ID, &p.ProjectID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.Schema, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update prompt schema: %w", err)
	}
	return p, nil
}

func RestorePrompt(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID) error {
	_, err := GetPromptByID(ctx, id, projectIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	result, err := db.Pool.Exec(ctx, "UPDATE prompts SET status = 'active' WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("restore prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MovePrompt relocates a prompt to the target project. The caller must have
// already authorized the move; projectIDs scopes which projects the prompt may
// currently belong to. A name or slug collision in the target project is
// rejected with ErrDuplicate.
func MovePrompt(ctx context.Context, id uuid.UUID, projectIDs []uuid.UUID, targetProjectID uuid.UUID) error {
	_, err := GetPromptByID(ctx, id, projectIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	_, err = db.Pool.Exec(ctx, "UPDATE prompts SET project_id = $1, updated_at = NOW() WHERE id = $2", targetProjectID, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicate
		}
		return fmt.Errorf("move prompt: %w", err)
	}
	return nil
}
