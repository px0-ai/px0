package handler

import (
	"errors"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createTeamRequest struct {
	Name  string     `json:"name"`
	OrgID *uuid.UUID `json:"org_id,omitempty"`
}

type updateTeamRequest struct {
	Name  string     `json:"name"`
	OrgID *uuid.UUID `json:"org_id,omitempty"`
}

type addTeamMemberRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

type updateTeamMemberRoleRequest struct {
	Role string `json:"role"`
}

func CreateTeam(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("orgID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isOrgAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req createTeamRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	exists, err := store.TeamNameExistsInOrg(c.Context(), orgID, req.Name)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if exists {
		return apierr.NewAPIError(fiber.StatusConflict, "team name already exists under this organization").Respond(c)
	}

	team, err := store.CreateTeamWithOrg(c.Context(), req.Name, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"team": team})
}

func UpdateTeam(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, id)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req updateTeamRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	targetOrgID := team.OrgID
	if req.OrgID != nil {
		targetOrgID = req.OrgID
	}

	if targetOrgID != nil {
		exists, err := store.TeamNameExistsInOrgForOther(c.Context(), id, *targetOrgID, req.Name)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if exists {
			return apierr.NewAPIError(fiber.StatusConflict, "team name already exists under this organization").Respond(c)
		}
	}

	updatedTeam, err := store.UpdateTeam(c.Context(), id, req.Name, req.OrgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"team": updatedTeam})
}

func AddTeamMember(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req addTeamMemberRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.UserID == uuid.Nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "user_id is required").Respond(c)
	}

	// Verify the user being added actually exists in the database
	_, err = store.GetUserByID(c.Context(), req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "user to add not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
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
	targetUserID, err := uuid.Parse(c.Params("userID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), teamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if team.OrgID != nil {
		inOrg, err := store.IsUserInOrg(c.Context(), targetUserID, *team.OrgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if !inOrg {
			return apierr.NewAPIError(fiber.StatusBadRequest, "user is not a member of this organization").Respond(c)
		}
	}

	if err := store.RemoveTeamMember(c.Context(), teamID, targetUserID); err != nil {
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

func ListTeamMembers(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isTeamViewer, err := store.IsTeamViewer(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isTeamViewer {
		return apierr.ErrForbidden.Respond(c)
	}

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page <= 0 {
		page = 1
	}
	limit := 10

	members, total, err := store.GetTeamMembersPaginated(c.Context(), teamID, page, limit)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if members == nil {
		members = []*model.TeamMemberResponse{}
	}

	return c.JSON(fiber.Map{
		"members": members,
		"page":    page,
		"limit":   limit,
		"total":   total,
	})
}

func UpdateTeamMemberRole(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	targetUserID, err := uuid.Parse(c.Params("userID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isTeamAdmin, err := store.IsTeamAdmin(c.Context(), userID, teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isTeamAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req updateTeamMemberRoleRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.Role != "admin" && req.Role != "editor" && req.Role != "viewer" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid role, must be admin, editor, or viewer").Respond(c)
	}

	err = store.UpdateTeamMemberRole(c.Context(), teamID, targetUserID, req.Role)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team member relationship not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "role updated successfully",
	})
}
