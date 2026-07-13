package handler

import (
	"bytes"
	"errors"
	"text/template"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type renderRequest struct {
	Variables map[string]any `json:"variables"`
}

// resolveProjectPromptBySlug loads the prompt identified by the :slug path
// parameter within the :projectID project, enforcing that the requester can
// reach that project. When it returns ok == false it has already written the
// appropriate error response to c and the caller should return nil.
func resolveProjectPromptBySlug(c *fiber.Ctx) (prompt *model.Prompt, ok bool) {
	projectID, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		_ = apierr.ErrInvalidID.Respond(c)
		return nil, false
	}
	slug := c.Params("slug")
	if slug == "" {
		_ = apierr.ErrPromptNotFound.Respond(c)
		return nil, false
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		_ = apierr.ErrInternalError.Respond(c, err)
		return nil, false
	}

	accessible := false
	for _, id := range projectIDs {
		if id == projectID {
			accessible = true
			break
		}
	}
	if !accessible {
		_ = apierr.ErrPromptNotFound.Respond(c)
		return nil, false
	}

	prompt, err = store.GetPromptBySlug(c.Context(), slug, []uuid.UUID{projectID})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = apierr.ErrPromptNotFound.Respond(c)
		} else {
			_ = apierr.ErrInternalError.Respond(c, err)
		}
		return nil, false
	}
	return prompt, true
}

func RenderLive(c *fiber.Ctx) error {
	prompt, ok := resolveProjectPromptBySlug(c)
	if !ok {
		return nil
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
	prompt, ok := resolveProjectPromptBySlug(c)
	if !ok {
		return nil
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

	tmpl, err := template.New("prompt").Option("missingkey=error").Parse(version.Template)
	if err != nil {
		return apierr.ErrTemplateParseError.Respond(c, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, req.Variables); err != nil {
		return apierr.ErrTemplateExecutionFailed.WithDetails(err.Error()).Respond(c, err)
	}

	return c.JSON(fiber.Map{
		"rendered":     buf.String(),
		"version":      version.Version,
		"slug":         prompt.Slug,
		"tags":         version.Tags,
		"model":        version.Model,
		"model_params": version.ModelParams,
	})
}
