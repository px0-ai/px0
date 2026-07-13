package handler

import (
	"context"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

// getRequestSubject extracts the Subject from locals.
func getRequestSubject(c *fiber.Ctx) (*model.Subject, error) {
	subjVal := c.Locals("subject")
	if subjVal == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	subj, ok := subjVal.(*model.Subject)
	if !ok {
		return nil, fmt.Errorf("unauthorized")
	}
	return subj, nil
}

// hasRequiredRole checks if the actual role meets or exceeds minRole.
func hasRequiredRole(actualRole, minRole string) bool {
	if actualRole == model.RoleAdmin {
		return true
	}
	if actualRole == model.RoleEditor && (minRole == model.RoleEditor || minRole == model.RoleViewer) {
		return true
	}
	if actualRole == model.RoleViewer && minRole == model.RoleViewer {
		return true
	}
	return false
}

// getSubjectTeams returns the team IDs for which the subject has at least the required role.
func getSubjectTeams(subj *model.Subject, minRole string) []uuid.UUID {
	var teamIDs []uuid.UUID
	for tID, role := range subj.TeamRoles {
		if hasRequiredRole(role, minRole) {
			teamIDs = append(teamIDs, tID)
		}
	}
	return teamIDs
}

// GetSubjectProjectIDs returns the project IDs the subject can access with at least minRole.
func GetSubjectProjectIDs(ctx context.Context, subj *model.Subject, minRole string) ([]uuid.UUID, error) {
	teamIDs := getSubjectTeams(subj, minRole)
	if len(teamIDs) == 0 {
		return []uuid.UUID{}, nil
	}
	return store.ListAccessibleProjectIDs(ctx, teamIDs)
}

// containsUUID reports whether target is present in ids.
func containsUUID(ids []uuid.UUID, target uuid.UUID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func getRequestTeamIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	subj, err := getRequestSubject(c)
	if err != nil {
		return nil, err
	}
	var teamIDs []uuid.UUID
	for tID := range subj.TeamRoles {
		teamIDs = append(teamIDs, tID)
	}
	return teamIDs, nil
}

func getRequestEditorTeamIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	subj, err := getRequestSubject(c)
	if err != nil {
		return nil, err
	}
	var teamIDs []uuid.UUID
	for tID, role := range subj.TeamRoles {
		if role == model.RoleEditor || role == model.RoleAdmin {
			teamIDs = append(teamIDs, tID)
		}
	}
	return teamIDs, nil
}

func getRequestAdminTeamIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	subj, err := getRequestSubject(c)
	if err != nil {
		return nil, err
	}
	var teamIDs []uuid.UUID
	for tID, role := range subj.TeamRoles {
		if role == model.RoleAdmin {
			teamIDs = append(teamIDs, tID)
		}
	}
	return teamIDs, nil
}

func getRequestViewerProjectIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return nil, err
	}
	return store.ListAccessibleProjectIDs(c.Context(), teamIDs)
}

func getRequestEditorProjectIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	teamIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return nil, err
	}
	return store.ListAccessibleProjectIDs(c.Context(), teamIDs)
}

func getRequestAdminProjectIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	teamIDs, err := getRequestAdminTeamIDs(c)
	if err != nil {
		return nil, err
	}
	return store.ListAccessibleProjectIDs(c.Context(), teamIDs)
}

func IsOrgAdmin(c *fiber.Ctx, orgID uuid.UUID) (bool, error) {
	subj, err := getRequestSubject(c)
	if err != nil {
		return false, err
	}
	if subj.IsUserAdmin {
		return true, nil
	}
	if subj.OrgID != nil && *subj.OrgID == orgID && subj.IsOrgAdmin {
		return true, nil
	}
	return false, nil
}

func IsTeamAdmin(c *fiber.Ctx, teamID uuid.UUID) (bool, error) {
	subj, err := getRequestSubject(c)
	if err != nil {
		return false, err
	}
	if subj.IsUserAdmin {
		return true, nil
	}
	if subj.IsOrgAdmin && subj.OrgID != nil {
		t, err := store.GetTeamByID(c.Context(), teamID)
		if err == nil && t.OrgID != nil && *t.OrgID == *subj.OrgID {
			return true, nil
		}
	}
	role, ok := subj.TeamRoles[teamID]
	return ok && role == model.RoleAdmin, nil
}

func IsTeamEditor(c *fiber.Ctx, teamID uuid.UUID) (bool, error) {
	subj, err := getRequestSubject(c)
	if err != nil {
		return false, err
	}
	if subj.IsUserAdmin {
		return true, nil
	}
	if subj.IsOrgAdmin && subj.OrgID != nil {
		t, err := store.GetTeamByID(c.Context(), teamID)
		if err == nil && t.OrgID != nil && *t.OrgID == *subj.OrgID {
			return true, nil
		}
	}
	role, ok := subj.TeamRoles[teamID]
	return ok && (role == model.RoleEditor || role == model.RoleAdmin), nil
}

func IsTeamViewer(c *fiber.Ctx, teamID uuid.UUID) (bool, error) {
	subj, err := getRequestSubject(c)
	if err != nil {
		return false, err
	}
	if subj.IsUserAdmin {
		return true, nil
	}
	if subj.IsOrgAdmin && subj.OrgID != nil {
		t, err := store.GetTeamByID(c.Context(), teamID)
		if err == nil && t.OrgID != nil && *t.OrgID == *subj.OrgID {
			return true, nil
		}
	}
	role, ok := subj.TeamRoles[teamID]
	return ok && (role == model.RoleViewer || role == model.RoleEditor || role == model.RoleAdmin), nil
}
