package handler

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/store"
)

func getRequestTeamIDs(c *fiber.Ctx) ([]uuid.UUID, error) {
	if teamID, ok := c.Locals("apiKeyTeamID").(uuid.UUID); ok {
		return []uuid.UUID{teamID}, nil
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
