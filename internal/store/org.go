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

func GetUserOrganizations(ctx context.Context, userID uuid.UUID) ([]*model.OrganizationWithRole, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT DISTINCT o.id, o.name, o.created_at
		 FROM organizations o
		 JOIN teams t ON t.org_id = o.id
		 JOIN team_members tm ON tm.team_id = t.id
		 WHERE tm.user_id = $1
		 ORDER BY o.created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get user organizations: %w", err)
	}
	defer rows.Close()

	var orgs []*model.OrganizationWithRole
	for rows.Next() {
		org := &model.OrganizationWithRole{}
		if err := rows.Scan(&org.ID, &org.Name, &org.CreatedAt); err != nil {
			return nil, err
		}

		// Determine the user's role in this organization
		isAdmin, err := IsOrgAdmin(ctx, userID, org.ID)
		if err != nil {
			return nil, err
		}
		if isAdmin {
			org.Role = "ADMIN"
		} else {
			org.Role = "MEMBER"
		}

		orgs = append(orgs, org)
	}
	return orgs, rows.Err()
}

func RemoveOrgMember(ctx context.Context, orgID, userID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`DELETE FROM team_members
		 WHERE user_id = $1 AND team_id IN (
			 SELECT id FROM teams WHERE org_id = $2
		 )`,
		userID, orgID,
	)
	if err != nil {
		return fmt.Errorf("remove organization member: %w", err)
	}
	return nil
}

func DeleteOrganization(ctx context.Context, id uuid.UUID) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete organization: %w", err)
	}
	return nil
}
