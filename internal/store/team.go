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

func CreateTeam(ctx context.Context, name string) (*model.Team, error) {
	t := &model.Team{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO teams (name) VALUES ($1)
		 RETURNING id, org_id, name, created_at`,
		name,
	).Scan(&t.ID, &t.OrgID, &t.Name, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}
	return t, nil
}

func CreateTeamWithOrg(ctx context.Context, name string, orgID uuid.UUID) (*model.Team, error) {
	t := &model.Team{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO teams (name, org_id) VALUES ($1, $2)
		 RETURNING id, org_id, name, created_at`,
		name, orgID,
	).Scan(&t.ID, &t.OrgID, &t.Name, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create team with org: %w", err)
	}
	return t, nil
}

func UpdateTeam(ctx context.Context, id uuid.UUID, name string, orgID *uuid.UUID) (*model.Team, error) {
	t := &model.Team{}
	err := db.Pool.QueryRow(ctx,
		`UPDATE teams SET name = $1, org_id = $2 WHERE id = $3
		 RETURNING id, org_id, name, created_at`,
		name, orgID, id,
	).Scan(&t.ID, &t.OrgID, &t.Name, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update team: %w", err)
	}
	return t, nil
}

func GetTeamByID(ctx context.Context, id uuid.UUID) (*model.Team, error) {
	t := &model.Team{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, org_id, name, created_at FROM teams WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.OrgID, &t.Name, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get team by id: %w", err)
	}
	return t, nil
}

func AddTeamMember(ctx context.Context, teamID, userID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO team_members (team_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		teamID, userID,
	)
	if err != nil {
		return fmt.Errorf("add team member: %w", err)
	}
	return nil
}

func RemoveTeamMember(ctx context.Context, teamID, userID uuid.UUID) error {
	_, err := db.Pool.Exec(ctx,
		`DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`,
		teamID, userID,
	)
	if err != nil {
		return fmt.Errorf("remove team member: %w", err)
	}
	return nil
}

func GetTeamMembers(ctx context.Context, teamID uuid.UUID) ([]*model.User, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT u.id, u.email, u.password_hash, u.is_admin, u.is_verified, u.created_at 
		 FROM users u 
		 JOIN team_members tm ON u.id = tm.user_id 
		 WHERE tm.team_id = $1`,
		teamID,
	)
	if err != nil {
		return nil, fmt.Errorf("get team members: %w", err)
	}
	defer rows.Close()

	var users []*model.User
	for rows.Next() {
		u := &model.User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func GetUserTeams(ctx context.Context, userID uuid.UUID) ([]*model.Team, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT t.id, t.org_id, t.name, t.created_at 
		 FROM teams t 
		 JOIN team_members tm ON t.id = tm.team_id 
		 WHERE tm.user_id = $1`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get user teams: %w", err)
	}
	defer rows.Close()

	var teams []*model.Team
	for rows.Next() {
		t := &model.Team{}
		if err := rows.Scan(&t.ID, &t.OrgID, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}
