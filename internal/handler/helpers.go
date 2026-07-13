package handler

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/store"
)

func getRequestTeamIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	if teamIDs, ok := c.Locals("apiKeyTeamIDs").([]uuid.UUID); ok {
		return teamIDs, nil
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return nil, fmt.Errorf("no user id in context")
	}

	teams, err := store.GetUserTeams(c.Context(), userID)
	if err != nil {
		return nil, err
	}

	var teamIDs []uuid.UUID
	for _, t := range teams {
		teamIDs = append(teamIDs, t.ID)
	}
	return teamIDs, nil
}

func getRequestEditorTeamIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	if teamIDs, ok := c.Locals("apiKeyTeamIDs").([]uuid.UUID); ok {
		if operation, ok := c.Locals("apiKeyOperation").(string); ok {
			if operation == "all" {
				return teamIDs, nil
			}
		}
		return nil, nil // 'read_render' operation key has no editor permissions
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return nil, fmt.Errorf("no user id in context")
	}

	teams, err := store.GetUserTeams(c.Context(), userID)
	if err != nil {
		return nil, err
	}

	var teamIDs []uuid.UUID
	for _, t := range teams {
		isEditor, err := store.IsTeamEditor(c.Context(), userID, t.ID)
		if err != nil {
			return nil, err
		}
		if isEditor {
			teamIDs = append(teamIDs, t.ID)
		}
	}
	return teamIDs, nil
}

func getRequestAdminTeamIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	if teamIDs, ok := c.Locals("apiKeyTeamIDs").([]uuid.UUID); ok {
		if operation, ok := c.Locals("apiKeyOperation").(string); ok {
			if operation == "all" {
				return teamIDs, nil
			}
		}
		return nil, nil // API Key must have full write access
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return nil, fmt.Errorf("no user id in context")
	}

	teams, err := store.GetUserTeams(c.Context(), userID)
	if err != nil {
		return nil, err
	}

	var teamIDs []uuid.UUID
	for _, t := range teams {
		var isOrgAdmin bool
		var err error
		if t.OrgID != nil {
			isOrgAdmin, err = store.IsOrgAdmin(c.Context(), userID, *t.OrgID)
			if err != nil {
				return nil, err
			}
		}
		isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, t.ID)
		if err != nil {
			return nil, err
		}
		if isOrgAdmin || isTeamAdmin {
			teamIDs = append(teamIDs, t.ID)
		}
	}
	return teamIDs, nil
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

// getRequestViewerProjectIDs returns the IDs of every project the requester can
// reach at viewer-or-above capability — their teams (owned + granted projects).
func getRequestViewerProjectIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return nil, err
	}
	return store.ListAccessibleProjectIDs(c.Context(), teamIDs)
}

// getRequestEditorProjectIDs returns the IDs of projects the requester can reach
// with editor-or-above capability.
func getRequestEditorProjectIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	teamIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return nil, err
	}
	return store.ListAccessibleProjectIDs(c.Context(), teamIDs)
}

// getRequestAdminProjectIDs returns the IDs of projects the requester can reach
// with admin capability.
func getRequestAdminProjectIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	teamIDs, err := getRequestAdminTeamIDs(c)
	if err != nil {
		return nil, err
	}
	return store.ListAccessibleProjectIDs(c.Context(), teamIDs)
}

func IsOrgAdmin(c *fiber.Ctx, orgID uuid.UUID) (bool, error) {
	if op, ok := c.Locals("apiKeyOperation").(string); ok {
		if op == "admin" || op == "all" {
			if keyOrgID, ok := c.Locals("apiKeyOrgID").(uuid.UUID); ok {
				return keyOrgID == orgID, nil
			}
		}
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return false, nil
	}
	return store.IsOrgAdmin(c.Context(), userID, orgID)
}

func IsTeamAdmin(c *fiber.Ctx, teamID uuid.UUID) (bool, error) {
	if op, ok := c.Locals("apiKeyOperation").(string); ok {
		if op == "admin" || op == "all" {
			if keyOrgID, ok := c.Locals("apiKeyOrgID").(uuid.UUID); ok {
				t, err := store.GetTeamByID(c.Context(), teamID)
				if err == nil && t.OrgID != nil && *t.OrgID == keyOrgID {
					return true, nil
				}
			}
		}
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return false, nil
	}
	return store.IsTeamAdmin(c.Context(), userID, teamID)
}

func IsTeamEditor(c *fiber.Ctx, teamID uuid.UUID) (bool, error) {
	if op, ok := c.Locals("apiKeyOperation").(string); ok {
		if op == "admin" || op == "all" {
			if keyOrgID, ok := c.Locals("apiKeyOrgID").(uuid.UUID); ok {
				t, err := store.GetTeamByID(c.Context(), teamID)
				if err == nil && t.OrgID != nil && *t.OrgID == keyOrgID {
					return true, nil
				}
			}
		}
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return false, nil
	}
	return store.IsTeamEditor(c.Context(), userID, teamID)
}

func IsTeamViewer(c *fiber.Ctx, teamID uuid.UUID) (bool, error) {
	if op, ok := c.Locals("apiKeyOperation").(string); ok {
		if op == "admin" || op == "all" || op == "read_render" {
			if keyOrgID, ok := c.Locals("apiKeyOrgID").(uuid.UUID); ok {
				t, err := store.GetTeamByID(c.Context(), teamID)
				if err == nil && t.OrgID != nil && *t.OrgID == keyOrgID {
					return true, nil
				}
			}
		}
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return false, nil
	}
	return store.IsTeamViewer(c.Context(), userID, teamID)
}
