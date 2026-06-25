package handler

import (
	"errors"
	"strconv"
	"text/template"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createVersionRequest struct {
	Template string `json:"template"`
}

type updateVersionRequest struct {
	Template string `json:"template"`
}

func CreateVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var req createVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Template == "" {
		return apierr.ErrTemplateRequired.Respond(c)
	}
	if _, err := template.New("").Parse(req.Template); err != nil {
		return apierr.ErrInvalidTemplate.WithDetails(err.Error()).Respond(c, err)
	}

	version, err := store.CreateVersion(c.Context(), promptID, req.Template)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": version})
}

func ListVersions(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	versions, err := store.ListVersions(c.Context(), promptID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if versions == nil {
		versions = []*model.PromptVersion{}
	}
	return c.JSON(fiber.Map{"versions": versions})
}

func GetVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}
	versionNum, err := strconv.Atoi(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	version, err := store.GetVersion(c.Context(), promptID, versionNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": version})
}

func UpdateVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}
	versionNum, err := strconv.Atoi(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	existing, err := store.GetVersion(c.Context(), promptID, versionNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	if existing.Status != model.VersionStatusDraft {
		return apierr.ErrOnlyDraftsModifiable.Respond(c)
	}

	var req updateVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Template == "" {
		return apierr.ErrTemplateRequired.Respond(c)
	}
	if _, err := template.New("").Parse(req.Template); err != nil {
		return apierr.ErrInvalidTemplate.WithDetails(err.Error()).Respond(c, err)
	}

	updated, err := store.UpdateVersionTemplate(c.Context(), existing.ID, req.Template)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": updated})
}

func PublishVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}
	versionNum, err := strconv.Atoi(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	version, err := store.PublishVersion(c.Context(), promptID, versionNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.NewAPIError(fiber.StatusUnprocessableEntity, err.Error()).Respond(c, err)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": version})
}
