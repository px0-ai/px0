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
	Status   *string
	Q        string
}

func CreatePrompt(ctx context.Context, teamID uuid.UUID, slug, name, description string) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO prompts (team_id, slug, name, description)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, team_id, slug, name, description, status, created_at, updated_at`,
		teamID, slug, name, description,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
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
	query := `SELECT DISTINCT p.id, p.team_id, p.slug, p.name, p.description, p.status, p.created_at, p.updated_at
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

	if filter.Q != "" {
		args = append(args, "%"+filter.Q+"%")
		param := fmt.Sprintf("$%d", len(args))
		whereClauses = append(whereClauses, fmt.Sprintf("(p.name ILIKE %s OR p.description ILIKE %s)", param, param))
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
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

func GetPromptByID(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, team_id, slug, name, description, status, created_at, updated_at
		 FROM prompts
		 WHERE id = $1 AND team_id = ANY($2)`,
		id, teamIDs,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

func GetPromptBySlug(ctx context.Context, slug string, teamIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, team_id, slug, name, description, status, created_at, updated_at
		 FROM prompts
		 WHERE slug = $1 AND team_id = ANY($2)`,
		slug, teamIDs,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt by slug: %w", err)
	}
	return p, nil
}

func ArchivePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) error {
	// First check if the prompt belongs to one of the provided teams
	_, err := GetPromptByID(ctx, id, teamIDs)
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

func UpdatePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID, description string) (*model.Prompt, error) {
	// First check if the prompt belongs to one of the allowed teams
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return nil, err // ErrNotFound if not found or no access
	}

	p := &model.Prompt{}
	err = db.Pool.QueryRow(ctx,
		`UPDATE prompts
		 SET description = $1, updated_at = NOW()
		 WHERE id = $2
		 RETURNING id, team_id, slug, name, description, status, created_at, updated_at`,
		description, id,
	).Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update prompt: %w", err)
	}
	return p, nil
}

func RestorePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) error {
	_, err := GetPromptByID(ctx, id, teamIDs)
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

func MovePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID, targetTeamID uuid.UUID) error {
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	_, err = db.Pool.Exec(ctx, "UPDATE prompts SET team_id = $1, updated_at = NOW() WHERE id = $2", targetTeamID, id)
	if err != nil {
		return fmt.Errorf("move prompt: %w", err)
	}
	return nil
}

// GetPromptsByIDs fetches full prompt records for the given IDs, scoped to
// the provided teamIDs for tenancy isolation.
//
// An optional status filter is applied as a store-layer safety net — the
// search provider may already have filtered by status, but we re-apply it
// here for defense-in-depth correctness.
//
// The returned slice preserves the order of the input ids slice so that the
// caller's score-based ranking (from the search provider) is maintained.
func GetPromptsByIDs(ctx context.Context, ids []uuid.UUID, teamIDs []uuid.UUID, status *string) ([]*model.Prompt, error) {
	args := []any{ids, teamIDs}
	where := "id = ANY($1) AND team_id = ANY($2)"

	if status != nil {
		args = append(args, *status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	rows, err := db.Pool.Query(ctx,
		fmt.Sprintf(`SELECT id, team_id, slug, name, description, status, created_at, updated_at
		             FROM prompts WHERE %s`, where),
		args...)
	if err != nil {
		return nil, fmt.Errorf("get prompts by ids: %w", err)
	}
	defer rows.Close()

	byID := make(map[uuid.UUID]*model.Prompt, len(ids))
	for rows.Next() {
		p := &model.Prompt{}
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Slug, &p.Name, &p.Description,
			&p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Re-order to match the original ids slice (score order from search provider).
	result := make([]*model.Prompt, 0, len(ids))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			result = append(result, p)
		}
	}
	return result, nil
}

