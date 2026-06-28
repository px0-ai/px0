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
	var count int
	err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM team_members WHERE team_id = $1`, teamID).Scan(&count)
	if err != nil {
		return fmt.Errorf("add team member count check: %w", err)
	}

	role := "editor"
	if count == 0 {
		role = "admin"
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		teamID, userID, role,
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

func GetTeamMembersPaginated(ctx context.Context, teamID uuid.UUID, page, limit int) ([]*model.TeamMemberResponse, int, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}
	offset := (page - 1) * limit

	var total int
	err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM team_members WHERE team_id = $1`, teamID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("get team members count: %w", err)
	}

	rows, err := db.Pool.Query(ctx,
		`SELECT u.id, u.email, tm.role, tm.created_at 
		 FROM users u 
		 JOIN team_members tm ON u.id = tm.user_id 
		 WHERE tm.team_id = $1 
		 ORDER BY tm.created_at ASC 
		 LIMIT $2 OFFSET $3`,
		teamID, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("get team members paginated: %w", err)
	}
	defer rows.Close()

	var members []*model.TeamMemberResponse
	for rows.Next() {
		m := &model.TeamMemberResponse{}
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.CreatedAt); err != nil {
			return nil, 0, err
		}
		members = append(members, m)
	}
	return members, total, rows.Err()
}

func UpdateTeamMemberRole(ctx context.Context, teamID, userID uuid.UUID, role string) error {
	result, err := db.Pool.Exec(ctx,
		`UPDATE team_members SET role = $1 WHERE team_id = $2 AND user_id = $3`,
		role, teamID, userID,
	)
	if err != nil {
		return fmt.Errorf("update team member role: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func TeamNameExistsInOrg(ctx context.Context, orgID uuid.UUID, name string) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM teams WHERE LOWER(name) = LOWER($1) AND org_id = $2)`,
		name, orgID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check team name exists in org: %w", err)
	}
	return exists, nil
}

func TeamNameExistsInOrgForOther(ctx context.Context, teamID uuid.UUID, orgID uuid.UUID, name string) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM teams WHERE LOWER(name) = LOWER($1) AND org_id = $2 AND id != $3)`,
		name, orgID, teamID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check team name exists in org for other: %w", err)
	}
	return exists, nil
}

func IsSystemAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	var isAdmin bool
	err := db.Pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1`, userID).Scan(&isAdmin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check system admin: %w", err)
	}
	return isAdmin, nil
}

func IsOrgAdmin(ctx context.Context, userID uuid.UUID, orgID uuid.UUID) (bool, error) {
	sysAdmin, err := IsSystemAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if sysAdmin {
		return true, nil
	}

	var exists bool
	err = db.Pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM team_members tm
			JOIN teams t ON tm.team_id = t.id
			WHERE tm.user_id = $1 AND t.org_id = $2 AND tm.role = 'admin' AND t.name = 'Default Team'
		)`,
		userID, orgID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check org admin: %w", err)
	}
	return exists, nil
}

func IsTeamAdmin(ctx context.Context, userID uuid.UUID, teamID uuid.UUID) (bool, error) {
	sysAdmin, err := IsSystemAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if sysAdmin {
		return true, nil
	}

	var exists bool
	err = db.Pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM team_members tm
			WHERE tm.user_id = $1 AND tm.team_id = $2 AND tm.role = 'admin'
		)`,
		userID, teamID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check team admin: %w", err)
	}
	return exists, nil
}

func IsTeamEditor(ctx context.Context, userID uuid.UUID, teamID uuid.UUID) (bool, error) {
	sysAdmin, err := IsSystemAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if sysAdmin {
		return true, nil
	}

	var exists bool
	err = db.Pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM team_members 
			WHERE user_id = $1 AND team_id = $2 AND role IN ('admin', 'editor')
		)`,
		userID, teamID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check team editor: %w", err)
	}
	return exists, nil
}

func IsTeamViewer(ctx context.Context, userID uuid.UUID, teamID uuid.UUID) (bool, error) {
	sysAdmin, err := IsSystemAdmin(ctx, userID)
	if err != nil {
		return false, err
	}
	if sysAdmin {
		return true, nil
	}

	var exists bool
	err = db.Pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM team_members 
			WHERE user_id = $1 AND team_id = $2
		)`,
		userID, teamID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check team viewer: %w", err)
	}
	return exists, nil
}

func IsUserInOrg(ctx context.Context, userID uuid.UUID, orgID uuid.UUID) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM team_members tm
			JOIN teams t ON tm.team_id = t.id
			WHERE tm.user_id = $1 AND t.org_id = $2
		)`,
		userID, orgID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check user in org: %w", err)
	}
	return exists, nil
}

func GetTeamsByOrgID(ctx context.Context, orgID uuid.UUID) ([]*model.Team, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, org_id, name, created_at FROM teams WHERE org_id = $1 ORDER BY name ASC`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("get teams by org id: %w", err)
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

func CreateJoinRequest(ctx context.Context, teamID, userID uuid.UUID) (*model.TeamJoinRequest, error) {
	r := &model.TeamJoinRequest{}
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO team_join_requests (team_id, user_id, status)
		 VALUES ($1, $2, 'pending')
		 RETURNING id, team_id, user_id, status, created_at, updated_at`,
		teamID, userID,
	).Scan(&r.ID, &r.TeamID, &r.UserID, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create join request: %w", err)
	}
	return r, nil
}

func GetJoinRequestByID(ctx context.Context, requestID uuid.UUID) (*model.TeamJoinRequest, error) {
	r := &model.TeamJoinRequest{}
	err := db.Pool.QueryRow(ctx,
		`SELECT id, team_id, user_id, status, created_at, updated_at FROM team_join_requests WHERE id = $1`,
		requestID,
	).Scan(&r.ID, &r.TeamID, &r.UserID, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get join request by id: %w", err)
	}
	return r, nil
}

func UpdateJoinRequestStatus(ctx context.Context, requestID uuid.UUID, status string) (*model.TeamJoinRequest, error) {
	r := &model.TeamJoinRequest{}
	err := db.Pool.QueryRow(ctx,
		`UPDATE team_join_requests
		 SET status = $1, updated_at = NOW()
		 WHERE id = $2
		 RETURNING id, team_id, user_id, status, created_at, updated_at`,
		status, requestID,
	).Scan(&r.ID, &r.TeamID, &r.UserID, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update join request status: %w", err)
	}
	return r, nil
}

func GetPendingJoinRequestsForAdmin(ctx context.Context, userID uuid.UUID) ([]*model.InboxItem, error) {
	sysAdmin, err := IsSystemAdmin(ctx, userID)
	if err != nil {
		return nil, err
	}

	rows, err := db.Pool.Query(ctx,
		`SELECT DISTINCT r.id, r.team_id, t.name AS team_name, r.user_id, u.email AS user_email, r.status, r.created_at, r.updated_at
		 FROM team_join_requests r
		 JOIN teams t ON r.team_id = t.id
		 JOIN users u ON r.user_id = u.id
		 WHERE r.status = 'pending'
		   AND (
		     $2::BOOLEAN = TRUE
		     OR EXISTS (
		       SELECT 1 FROM team_members tm
		       WHERE tm.user_id = $1 AND tm.team_id = r.team_id AND tm.role = 'admin'
		     )
		     OR EXISTS (
		       SELECT 1 FROM team_members tm2
		       JOIN teams t2 ON tm2.team_id = t2.id
		       WHERE tm2.user_id = $1 AND tm2.role = 'admin' AND t2.org_id = t.org_id AND t.org_id IS NOT NULL
		     )
		   )
		 ORDER BY r.created_at DESC`,
		userID, sysAdmin,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending join requests for admin: %w", err)
	}
	defer rows.Close()

	var items []*model.InboxItem
	for rows.Next() {
		item := &model.InboxItem{}
		if err := rows.Scan(&item.ID, &item.TeamID, &item.TeamName, &item.UserID, &item.UserEmail, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func DeleteTeam(ctx context.Context, id uuid.UUID) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	return nil
}

func GetOrgPeoplePaginated(ctx context.Context, orgID uuid.UUID, page, limit int) ([]*model.User, int, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 10
	}
	offset := (page - 1) * limit

	var total int
	err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT tm.user_id)
		 FROM team_members tm
		 JOIN teams t ON tm.team_id = t.id
		 WHERE t.org_id = $1 AND t.org_id IS NOT NULL`,
		orgID,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("get org people count: %w", err)
	}

	rows, err := db.Pool.Query(ctx,
		`SELECT DISTINCT u.id, u.email, u.password_hash, u.is_admin, u.is_verified, u.created_at
		 FROM users u
		 JOIN team_members tm ON u.id = tm.user_id
		 JOIN teams t ON tm.team_id = t.id
		 WHERE t.org_id = $1 AND t.org_id IS NOT NULL
		 ORDER BY u.created_at ASC
		 LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("get org people paginated: %w", err)
	}
	defer rows.Close()

	var users []*model.User
	for rows.Next() {
		u := &model.User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}
