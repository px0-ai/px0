package handler

import (
	"errors"
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/store"
)

var tagRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)

type setTagRequest struct {
	Tag string `json:"tag"`
}

func SetTag(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorTeamIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorTeamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var req setTagRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Tag == "" {
		return apierr.ErrTagRequired.Respond(c)
	}
	if len(req.Tag) > 50 || !tagRegex.MatchString(req.Tag) {
		return apierr.ErrInvalidTag.Respond(c)
	}

	version, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	err = store.SetTag(c.Context(), promptID, version.Version, req.Tag)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Retrieve the updated version with newly populated tags
	updatedVersion, err := store.GetVersion(c.Context(), promptID, version.Version)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": updatedVersion})
}

func RemoveTag(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}
	tag := c.Params("tag")
	if tag == "" {
		return apierr.ErrTagRequired.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorTeamIDs, err := getRequestEditorTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorTeamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	err = store.RemoveTag(c.Context(), promptID, tag)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrTagNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

type listTagsItem struct {
	Tag     string `json:"tag"`
	Version int    `json:"version"`
}

func ListTags(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, teamIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	tagMap, err := store.GetTagsForPrompt(c.Context(), promptID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	tags := []listTagsItem{}
	for version, versionTags := range tagMap {
		for _, tag := range versionTags {
			tags = append(tags, listTagsItem{
				Tag:     tag,
				Version: version,
			})
		}
	}

	return c.JSON(fiber.Map{"tags": tags})
}
