package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

// CreateProject inserts a project owned by owningTeamID. A colliding name or
// slug within the same owning team returns ErrDuplicate.
func CreateProject(ctx context.Context, owningTeamID uuid.UUID, slug, name string) (*model.Project, error) {
	p := &model.Project{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO projects (owning_team_id, slug, name)
		 VALUES ($1, $2, $3)
		 RETURNING id, owning_team_id, slug, name, created_at`,
		owningTeamID, slug, name,
	).Scan(&p.ID, &p.OwningTeamID, &p.Slug, &p.Name, &p.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicate
		}
		return nil, fmt.Errorf("create project: %w", err)
	}
	return p, nil
}

// GetProjectByID returns the project with the given id, or ErrNotFound.
func GetProjectByID(ctx context.Context, id uuid.UUID) (*model.Project, error) {
	p := &model.Project{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, owning_team_id, slug, name, created_at FROM projects WHERE id = $1`,
		id,
	).Scan(&p.ID, &p.OwningTeamID, &p.Slug, &p.Name, &p.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get project by id: %w", err)
	}
	return p, nil
}

// ListProjectsForTeam returns the projects a team owns plus those granted to it,
// most recent first.
func ListProjectsForTeam(ctx context.Context, teamID uuid.UUID) ([]*model.Project, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT DISTINCT p.id, p.owning_team_id, p.slug, p.name, p.created_at
		 FROM projects p
		 LEFT JOIN project_team_access pta ON pta.project_id = p.id
		 WHERE p.owning_team_id = $1 OR pta.team_id = $1
		 ORDER BY p.created_at DESC`,
		teamID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects for team: %w", err)
	}
	defer rows.Close()

	var projects []*model.Project
	for rows.Next() {
		p := &model.Project{}
		if err := rows.Scan(&p.ID, &p.OwningTeamID, &p.Slug, &p.Name, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// ListAccessibleProjectIDs returns the IDs of all projects reachable by the
// given teams — those the teams own plus those granted to them. This is the
// single expansion point from a requester's teams to the projects (and thus
// prompts) they can reach; the capability the requester holds is determined by
// which team-role set (viewer/editor/admin) the team IDs were drawn from.
func ListAccessibleProjectIDs(ctx context.Context, teamIDs []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT DISTINCT p.id
		 FROM projects p
		 LEFT JOIN project_team_access pta ON pta.project_id = p.id
		 WHERE p.owning_team_id = ANY($1) OR pta.team_id = ANY($1)`,
		teamIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("list accessible project ids: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GrantProjectAccess records that teamID may work with the project's prompts.
// It is idempotent (re-granting is a no-op) and returns ErrNotFound if the
// project or team does not exist.
func GrantProjectAccess(ctx context.Context, projectID, teamID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO project_team_access (project_id, team_id)
		 VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`,
		projectID, teamID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrNotFound
		}
		return fmt.Errorf("grant project access: %w", err)
	}
	return nil
}

// RevokeProjectAccess removes a team's access grant. Returns ErrNotFound if no
// such grant existed.
func RevokeProjectAccess(ctx context.Context, projectID, teamID uuid.UUID) error {
	result, err := db.Pool.Exec(ctx,
		`DELETE FROM project_team_access WHERE project_id = $1 AND team_id = $2`,
		projectID, teamID,
	)
	if err != nil {
		return fmt.Errorf("revoke project access: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IsProjectAccessibleByTeams reports whether the project is reachable by any of
// the given teams — either as the owning team or via an access grant.
func IsProjectAccessibleByTeams(ctx context.Context, projectID uuid.UUID, teamIDs []uuid.UUID) (bool, error) {
	var accessible bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM projects p
			LEFT JOIN project_team_access pta ON pta.project_id = p.id
			WHERE p.id = $1 AND (p.owning_team_id = ANY($2) OR pta.team_id = ANY($2))
		)`,
		projectID, teamIDs,
	).Scan(&accessible)
	if err != nil {
		return false, fmt.Errorf("check project accessible by teams: %w", err)
	}
	return accessible, nil
}

// DeleteProject hard-deletes a project, cascading to its prompts and grants.
// Returns ErrNotFound if no project matched.
func DeleteProject(ctx context.Context, id uuid.UUID) error {
	result, err := db.Pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
