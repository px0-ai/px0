package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createTeamRequest struct {
	Name string `json:"name"`
}

type addTeamMemberRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

func CreateTeam(c *fiber.Ctx) error {
	var req createTeamRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	team, err := store.CreateTeam(c.Context(), req.Name)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"team": team})
}

func AddTeamMember(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	var req addTeamMemberRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.UserID == uuid.Nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "user_id is required").Respond(c)
	}

	if err := store.AddTeamMember(c.Context(), teamID, req.UserID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func RemoveTeamMember(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	userID, err := uuid.Parse(c.Params("userID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	if err := store.RemoveTeamMember(c.Context(), teamID, userID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func ListUserTeams(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	teams, err := store.GetUserTeams(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if teams == nil {
		teams = []*model.Team{}
	}
	return c.JSON(fiber.Map{"teams": teams})
}
