package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/store"
)

type createOrgRequest struct {
	Name string `json:"name"`
}

type updateOrgRequest struct {
	Name string `json:"name"`
}

func CreateOrg(c *fiber.Ctx) error {
	var req createOrgRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
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

	var req updateOrgRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
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
