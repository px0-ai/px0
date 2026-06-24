package handler

import (
	"errors"
	"strconv"
	"text/template"

	"github.com/arpitbhayani/px0/internal/model"
	"github.com/arpitbhayani/px0/internal/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
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
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid prompt id"})
	}

	if _, err := store.GetPromptByID(c.Context(), promptID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "prompt not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	var req createVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Template == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template is required"})
	}
	if _, err := template.New("").Parse(req.Template); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid template: " + err.Error()})
	}

	version, err := store.CreateVersion(c.Context(), promptID, req.Template)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": version})
}

func ListVersions(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid prompt id"})
	}

	if _, err := store.GetPromptByID(c.Context(), promptID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "prompt not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	versions, err := store.ListVersions(c.Context(), promptID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if versions == nil {
		versions = []*model.PromptVersion{}
	}
	return c.JSON(fiber.Map{"versions": versions})
}

func GetVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid prompt id"})
	}
	versionNum, err := strconv.Atoi(c.Params("version"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid version number"})
	}

	version, err := store.GetVersion(c.Context(), promptID, versionNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "version not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"version": version})
}

func UpdateVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid prompt id"})
	}
	versionNum, err := strconv.Atoi(c.Params("version"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid version number"})
	}

	existing, err := store.GetVersion(c.Context(), promptID, versionNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "version not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if existing.Status != model.VersionStatusDraft {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "only draft versions can be modified",
		})
	}

	var req updateVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Template == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "template is required"})
	}
	if _, err := template.New("").Parse(req.Template); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid template: " + err.Error()})
	}

	updated, err := store.UpdateVersionTemplate(c.Context(), existing.ID, req.Template)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"version": updated})
}

func PublishVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid prompt id"})
	}
	versionNum, err := strconv.Atoi(c.Params("version"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid version number"})
	}

	version, err := store.PublishVersion(c.Context(), promptID, versionNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "version not found"})
		}
		if errors.Is(err, store.ErrConflict) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"version": version})
}
