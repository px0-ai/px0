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
