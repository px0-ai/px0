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

type createOrgRequest struct {
	Name string `json:"name"`
}

type updateOrgRequest struct {
	Name string `json:"name"`
}

func CreateOrg(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isSysAdmin, err := store.IsSystemAdmin(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isSysAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req createOrgRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	exists, err := store.OrganizationNameExists(c.Context(), req.Name)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if exists {
		return apierr.NewAPIError(fiber.StatusConflict, "organization name already exists").Respond(c)
	}

	org, err := store.CreateOrganization(c.Context(), req.Name)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"org": org})
}

func UpdateOrg(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, id)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isOrgAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	var req updateOrgRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	exists, err := store.OrganizationNameExistsForOther(c.Context(), id, req.Name)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if exists {
		return apierr.NewAPIError(fiber.StatusConflict, "organization name already exists").Respond(c)
	}

	org, err := store.UpdateOrganization(c.Context(), id, req.Name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "organization not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"org": org})
}

func ListUserOrgs(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	orgs, err := store.GetUserOrganizations(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if orgs == nil {
		orgs = []*model.OrganizationWithRole{}
	}
	return c.JSON(fiber.Map{"organizations": orgs})
}

func ListOrgPeople(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("orgID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	// Verify the caller is in the organization (or system admin)
	belongs, err := store.IsUserInOrg(c.Context(), userID, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !belongs {
		isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, orgID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if !isOrgAdmin {
			return apierr.ErrForbidden.Respond(c)
		}
	}

	page, _ := strconv.Atoi(c.Query("page", "1"))
	if page <= 0 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit", "10"))
	if limit <= 0 {
		limit = 10
	}

	people, total, err := store.GetOrgPeoplePaginated(c.Context(), orgID, page, limit)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if people == nil {
		people = []*model.User{}
	}

	return c.JSON(fiber.Map{
		"people": people,
		"page":   page,
		"limit":  limit,
		"total":  total,
	})
}

func RemoveOrgMember(c *fiber.Ctx) error {
	orgID, err := uuid.Parse(c.Params("orgID"))
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

	// Verify the caller is an Org Admin (or system admin)
	isOrgAdmin, err := store.IsOrgAdmin(c.Context(), userID, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isOrgAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	// Verify target user is in the organization
	inOrg, err := store.IsUserInOrg(c.Context(), targetUserID, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !inOrg {
		return apierr.NewAPIError(fiber.StatusNotFound, "user is not a member of this organization").Respond(c)
	}

	if err := store.RemoveOrgMember(c.Context(), orgID, targetUserID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}
