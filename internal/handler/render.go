package handler

import (
	"bytes"
	"errors"
	"text/template"

	"github.com/gofiber/fiber/v2"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type renderRequest struct {
	Variables map[string]any `json:"variables"`
}

func RenderLive(c *fiber.Ctx) error {
	slug := c.Params("slug")
	if slug == "" {
		return apierr.ErrPromptNotFound.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptBySlug(c.Context(), slug, teamIDs)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := store.GetLiveVersion(c.Context(), prompt.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrNoLiveVersionFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return executeRender(c, prompt, version)
}

func RenderVersion(c *fiber.Ctx) error {
	slug := c.Params("slug")
	if slug == "" {
		return apierr.ErrPromptNotFound.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptBySlug(c.Context(), slug, teamIDs)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := resolveVersion(c.Context(), prompt.ID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return executeRender(c, prompt, version)
}

func executeRender(c *fiber.Ctx, prompt *model.Prompt, version *model.PromptVersion) error {
	var req renderRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Variables == nil {
		req.Variables = map[string]any{}
	}

	tmpl, err := template.New("prompt").Parse(version.Template)
	if err != nil {
		return apierr.ErrTemplateParseError.Respond(c, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, req.Variables); err != nil {
		return apierr.ErrTemplateExecutionFailed.WithDetails(err.Error()).Respond(c, err)
	}

	return c.JSON(fiber.Map{
		"rendered": buf.String(),
		"version":  version.Version,
		"slug":     prompt.Slug,
		"tags":     version.Tags,
	})
}
