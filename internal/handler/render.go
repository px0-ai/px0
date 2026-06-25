package handler

import (
	"bytes"
	"errors"
	"strconv"
	"text/template"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/store"
)

type renderRequest struct {
	Variables map[string]any `json:"variables"`
}

func RenderLive(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c)
	}

	version, err := store.GetLiveVersion(c.Context(), promptID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrNoLiveVersionFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c)
	}

	return executeRender(c, version.Template, version.Version)
}

func RenderVersion(c *fiber.Ctx) error {
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
		return apierr.ErrInternalError.Respond(c)
	}

	return executeRender(c, version.Template, version.Version)
}

func executeRender(c *fiber.Ctx, tmplStr string, versionNum int) error {
	var req renderRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Variables == nil {
		req.Variables = map[string]any{}
	}

	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		return apierr.ErrTemplateParseError.Respond(c)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, req.Variables); err != nil {
		return apierr.ErrTemplateExecutionFailed.WithDetails(err.Error()).Respond(c)
	}

	return c.JSON(fiber.Map{
		"rendered": buf.String(),
		"version":  versionNum,
	})
}
