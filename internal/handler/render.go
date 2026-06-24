package handler

import (
	"bytes"
	"errors"
	"strconv"
	"text/template"

	"github.com/arpitbhayani/px0/internal/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type renderRequest struct {
	Variables map[string]any `json:"variables"`
}

func RenderLive(c *fiber.Ctx) error {
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

	version, err := store.GetLiveVersion(c.Context(), promptID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "no live version found for this prompt"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	return executeRender(c, version.Template, version.Version)
}

func RenderVersion(c *fiber.Ctx) error {
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

	return executeRender(c, version.Template, version.Version)
}

func executeRender(c *fiber.Ctx, tmplStr string, versionNum int) error {
	var req renderRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Variables == nil {
		req.Variables = map[string]any{}
	}

	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "template parse error"})
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, req.Variables); err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "template execution failed: " + err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"rendered": buf.String(),
		"version":  versionNum,
	})
}
