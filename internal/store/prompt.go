package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

func CreatePrompt(ctx context.Context, name, description string, teamIDs []uuid.UUID) (*model.Prompt, error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	p := &model.Prompt{}
	err = tx.QueryRow(ctx,
		`INSERT INTO prompts (name, description)
		 VALUES ($1, $2)
		 RETURNING id, name, description, created_at, updated_at`,
		name, description,
	).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create prompt: %w", err)
	}

	for _, teamID := range teamIDs {
		_, err = tx.Exec(ctx,
			`INSERT INTO prompt_teams (prompt_id, team_id) VALUES ($1, $2)`,
			p.ID, teamID,
		)
		if err != nil {
			return nil, fmt.Errorf("add prompt team: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return p, nil
}

func ListPrompts(ctx context.Context, teamIDs []uuid.UUID) ([]*model.Prompt, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT DISTINCT p.id, p.name, p.description, p.created_at, p.updated_at
		 FROM prompts p
		 JOIN prompt_teams pt ON p.id = pt.prompt_id
		 WHERE pt.team_id = ANY($1)
		 ORDER BY p.created_at DESC`,
		teamIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

	var prompts []*model.Prompt
	for rows.Next() {
		p := &model.Prompt{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}

func GetPromptByID(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT DISTINCT p.id, p.name, p.description, p.created_at, p.updated_at
		 FROM prompts p
		 JOIN prompt_teams pt ON p.id = pt.prompt_id
		 WHERE p.id = $1 AND pt.team_id = ANY($2)`,
		id, teamIDs,
	).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

func DeletePrompt(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) error {
	// First check if the prompt belongs to one of the provided teams
	_, err := GetPromptByID(ctx, id, teamIDs)
	if err != nil {
		return err // ErrNotFound if not found or no access
	}

	result, err := db.Pool.Exec(ctx, "DELETE FROM prompts WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func AddPromptTeam(ctx context.Context, promptID, teamID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO prompt_teams (prompt_id, team_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		promptID, teamID,
	)
	if err != nil {
		return fmt.Errorf("add prompt team: %w", err)
	}
	return nil
}

func RemovePromptTeam(ctx context.Context, promptID, teamID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`DELETE FROM prompt_teams WHERE prompt_id = $1 AND team_id = $2`,
		promptID, teamID,
	)
	if err != nil {
		return fmt.Errorf("remove prompt team: %w", err)
	}
	return nil
}
