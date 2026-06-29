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

func CreatePromptPayload(ctx context.Context, promptID uuid.UUID, variables []byte) (*model.PromptPayload, error) {
	p := &model.PromptPayload{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO prompt_payloads (prompt_id, variables)
		 VALUES ($1, $2)
		 RETURNING id, prompt_id, name, variables, created_at, updated_at`,
		promptID, variables,
	).Scan(&p.ID, &p.PromptID, &p.Name, &p.Variables, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create prompt payload: %w", err)
	}
	return p, nil
}

func GetPromptPayload(ctx context.Context, id uuid.UUID, promptID uuid.UUID) (*model.PromptPayload, error) {
	p := &model.PromptPayload{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, prompt_id, name, variables, created_at, updated_at
		 FROM prompt_payloads
		 WHERE id = $1 AND prompt_id = $2`,
		id, promptID,
	).Scan(&p.ID, &p.PromptID, &p.Name, &p.Variables, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get prompt payload: %w", err)
	}
	return p, nil
}

func ListPromptPayloads(ctx context.Context, promptID uuid.UUID) ([]*model.PromptPayload, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, prompt_id, name, variables, created_at, updated_at
		 FROM prompt_payloads
		 WHERE prompt_id = $1
		 ORDER BY created_at DESC`,
		promptID,
	)
	if err != nil {
		return nil, fmt.Errorf("list prompt payloads: %w", err)
	}
	defer rows.Close()

	var payloads []*model.PromptPayload
	for rows.Next() {
		p := &model.PromptPayload{}
		if err := rows.Scan(&p.ID, &p.PromptID, &p.Name, &p.Variables, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		payloads = append(payloads, p)
	}
	return payloads, rows.Err()
}

func UpdatePromptPayload(ctx context.Context, id uuid.UUID, promptID uuid.UUID, name *string, variables []byte) (*model.PromptPayload, error) {
	var query string
	var args []interface{}

	if name != nil && len(variables) > 0 {
		query = `UPDATE prompt_payloads
		         SET name = $1, variables = $2, updated_at = NOW()
		         WHERE id = $3 AND prompt_id = $4
		         RETURNING id, prompt_id, name, variables, created_at, updated_at`
		args = []interface{}{name, variables, id, promptID}
	} else if name != nil {
		query = `UPDATE prompt_payloads
		         SET name = $1, updated_at = NOW()
		         WHERE id = $2 AND prompt_id = $3
		         RETURNING id, prompt_id, name, variables, created_at, updated_at`
		args = []interface{}{name, id, promptID}
	} else if len(variables) > 0 {
		query = `UPDATE prompt_payloads
		         SET variables = $1, updated_at = NOW()
		         WHERE id = $2 AND prompt_id = $3
		         RETURNING id, prompt_id, name, variables, created_at, updated_at`
		args = []interface{}{variables, id, promptID}
	} else {
		return GetPromptPayload(ctx, id, promptID)
	}

	p := &model.PromptPayload{}
	err := db.Pool.QueryRow(ctx, query, args...).Scan(&p.ID, &p.PromptID, &p.Name, &p.Variables, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update prompt payload: %w", err)
	}
	return p, nil
}

func DeletePromptPayload(ctx context.Context, id uuid.UUID, promptID uuid.UUID) error {
	result, err := db.Pool.Exec(ctx,
		`DELETE FROM prompt_payloads WHERE id = $1 AND prompt_id = $2`,
		id, promptID,
	)
	if err != nil {
		return fmt.Errorf("delete prompt payload: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
