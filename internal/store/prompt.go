package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/arpitbhayani/px0/internal/db"
	"github.com/arpitbhayani/px0/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func CreatePrompt(ctx context.Context, name, description string) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO prompts (name, description)
		 VALUES ($1, $2)
		 RETURNING id, name, description, created_at, updated_at`,
		name, description,
	).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create prompt: %w", err)
	}
	return p, nil
}

func ListPrompts(ctx context.Context) ([]*model.Prompt, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, name, description, created_at, updated_at
		 FROM prompts ORDER BY created_at DESC`,
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

func GetPromptByID(ctx context.Context, id uuid.UUID) (*model.Prompt, error) {
	p := &model.Prompt{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, name, description, created_at, updated_at
		 FROM prompts WHERE id = $1`,
		id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt: %w", err)
	}
	return p, nil
}

func DeletePrompt(ctx context.Context, id uuid.UUID) error {
	result, err := db.Pool.Exec(ctx, "DELETE FROM prompts WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
