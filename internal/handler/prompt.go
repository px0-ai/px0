package handler

import (
	"errors"
	"strconv"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createPromptRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Slug        string `json:"slug"`
}

func NormalizeSlug(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	lastUnderscore := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
			lastUnderscore = false
		} else if r == '_' {
			if !lastUnderscore {
				sb.WriteRune('_')
				lastUnderscore = true
			}
		} else {
			if sb.Len() > 0 && !lastUnderscore {
				sb.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	res := sb.String()
	res = strings.Trim(res, "_")
	return res
}

func CreatePrompt(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("teamID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	allowedIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	isAllowed := false
	for _, id := range allowedIDs {
		if id == teamID {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		return apierr.ErrForbidden.Respond(c)
	}

	var req createPromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	// Normalize slug if provided; otherwise, generate from name and normalize.
	slug := req.Slug
	if slug == "" {
		slug = req.Name
	}
	slug = NormalizeSlug(slug)

	prompt, err := store.CreatePrompt(c.Context(), teamID, slug, req.Name, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "prompt with this name or slug already exists; please provide a unique name").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"prompt": prompt})
}

func ListPrompts(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("teamID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	allowedIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	isAllowed := false
	for _, id := range allowedIDs {
		if id == teamID {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		return apierr.ErrForbidden.Respond(c)
	}

	var tags []string
	tagsStr := c.Query("tags")
	if tagsStr != "" {
		parts := strings.Split(tagsStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				tags = append(tags, part)
			}
		}
	}

	var archived *bool
	archivedStr := c.Query("archived")
	if archivedStr != "" {
		if val, err := strconv.ParseBool(archivedStr); err == nil {
			archived = &val
		}
	}

	prompts, err := store.ListPrompts(c.Context(), store.PromptFilter{
		TeamIDs:  []uuid.UUID{teamID},
		Tags:     tags,
		Archived: archived,
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts})
}

func GetPrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptByID(c.Context(), id, teamIDs)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

func ArchivePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	adminTeamIDs, err := getRequestAdminTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.ArchivePrompt(c.Context(), id, adminTeamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptByID(c.Context(), id, teamIDs)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

func ListAllPrompts(c *fiber.Ctx) error {
	teamIDStr := c.Query("team")
	if teamIDStr == "" {
		teamIDStr = c.Query("team_id")
	}

	if teamIDStr == "" {
		// By default nothing is shown as per backwards-compatible test requirements
		return c.JSON(fiber.Map{"prompts": []*model.Prompt{}})
	}

	teamID, err := uuid.Parse(teamIDStr)
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	allowedIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	isAllowed := false
	for _, id := range allowedIDs {
		if id == teamID {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		return apierr.ErrForbidden.Respond(c)
	}

	var tags []string
	tagsStr := c.Query("tags")
	if tagsStr != "" {
		parts := strings.Split(tagsStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				tags = append(tags, part)
			}
		}
	}

	var archived *bool
	archivedStr := c.Query("archived")
	if archivedStr != "" {
		if val, err := strconv.ParseBool(archivedStr); err == nil {
			archived = &val
		}
	}

	prompts, err := store.ListPrompts(c.Context(), store.PromptFilter{
		TeamIDs:  []uuid.UUID{teamID},
		Tags:     tags,
		Archived: archived,
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts})
}
