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

func CreateOrganization(ctx context.Context, name string) (*model.Organization, error) {
	o := &model.Organization{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO organizations (name) VALUES ($1)
		 RETURNING id, name, created_at`,
		name,
	).Scan(&o.ID, &o.Name, &o.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create organization: %w", err)
	}
	return o, nil
}

func UpdateOrganization(ctx context.Context, id uuid.UUID, name string) (*model.Organization, error) {
	o := &model.Organization{}
	err := db.Pool.QueryRow(ctx,
		`UPDATE organizations SET name = $1 WHERE id = $2
		 RETURNING id, name, created_at`,
		name, id,
	).Scan(&o.ID, &o.Name, &o.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update organization: %w", err)
	}
	return o, nil
}

func GetOrganizationByID(ctx context.Context, id uuid.UUID) (*model.Organization, error) {
	o := &model.Organization{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, name, created_at FROM organizations WHERE id = $1`,
		id,
	).Scan(&o.ID, &o.Name, &o.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get organization by id: %w", err)
	}
	return o, nil
}

func OrganizationNameExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM organizations WHERE LOWER(name) = LOWER($1))`,
		name,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check organization name exists: %w", err)
	}
	return exists, nil
}

func OrganizationNameExistsForOther(ctx context.Context, id uuid.UUID, name string) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM organizations WHERE LOWER(name) = LOWER($1) AND id != $2)`,
		name, id,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check organization name exists for other: %w", err)
	}
	return exists, nil
}
