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
	TeamIDs  []uuid.UUID
	Tags     []string
	Archived *bool
}

func CreatePrompt(ctx context.Context, teamID uuid.UUID, slug, name, description string) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO prompts (team_id, slug, name, description)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, team_id, slug, name, description, is_archived, created_at, updated_at`,
		teamID, slug, name, description,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.IsArchived, &p.CreatedAt, &p.UpdatedAt)
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
	query := `SELECT DISTINCT p.id, p.team_id, p.slug, p.name, p.description, p.is_archived, p.created_at, p.updated_at
			  FROM prompts p`
	args := []any{}
	joins := ""
	whereClauses := []string{}

	if len(filter.Tags) > 0 {
		joins += " INNER JOIN prompt_tags pt ON pt.prompt_id = p.id"
		args = append(args, filter.Tags)
		whereClauses = append(whereClauses, fmt.Sprintf("pt.tag = ANY($%d)", len(args)))
	}

	if len(filter.TeamIDs) > 0 {
		args = append(args, filter.TeamIDs)
		whereClauses = append(whereClauses, fmt.Sprintf("p.team_id = ANY($%d)", len(args)))
	}

	if filter.Archived != nil {
		args = append(args, *filter.Archived)
		whereClauses = append(whereClauses, fmt.Sprintf("p.is_archived = $%d", len(args)))
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
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.IsArchived, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

func GetPromptByID(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, team_id, slug, name, description, is_archived, created_at, updated_at
		 FROM prompts
		 WHERE id = $1 AND team_id = ANY($2)`,
		id, teamIDs,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.IsArchived, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

func ArchivePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) error {
	// First check if the prompt belongs to one of the provided teams
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	result, err := db.Pool.Exec(ctx, "UPDATE prompts SET is_archived = TRUE WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("archive prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
