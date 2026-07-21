package handler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/pmezard/go-difflib/difflib"

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

// authorizeProject verifies the project is reachable by the requester at the
// given capability (allowedIDs is the set of project IDs at that capability).
// When it returns false it has already written a 404 (unknown project) or 403
// (project exists but not accessible at this capability) response.
func authorizeProject(c *fiber.Ctx, projectID uuid.UUID, allowedIDs []uuid.UUID) bool {
	if containsUUID(allowedIDs, projectID) {
		return true
	}
	if _, err := store.GetProjectByID(c.Context(), projectID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = apierr.ErrProjectNotFound.Respond(c)
		} else {
			_ = apierr.ErrInternalError.Respond(c, err)
		}
		return false
	}
	_ = apierr.ErrForbidden.Respond(c)
	return false
}

func CreatePrompt(c *fiber.Ctx) error {
	projectID, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	editorIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !authorizeProject(c, projectID, editorIDs) {
		return nil
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

	prompt, err := store.CreatePrompt(c.Context(), projectID, slug, req.Name, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "prompt with this name or slug already exists; please provide a unique name").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"prompt": prompt})
}

func ListPrompts(c *fiber.Ctx) error {
	projectID, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	viewerIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !authorizeProject(c, projectID, viewerIDs) {
		return nil
	}

	var status *string
	statusStr := c.Query("status")
	if statusStr != "" {
		status = &statusStr
	}

	var archived *bool
	archivedStr := c.Query("archived")
	if archivedStr != "" {
		if val, err := strconv.ParseBool(archivedStr); err == nil {
			archived = &val
		}
	}

	prompts, err := store.ListPrompts(c.Context(), store.PromptFilter{
		ProjectIDs: []uuid.UUID{projectID},
		Archived:   archived,
		Status:     status,
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
	param := c.Params("id")
	var prompt *model.Prompt
	var err error

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	id, err := uuid.Parse(param)
	if err == nil {
		prompt, err = store.GetPromptByID(c.Context(), id, projectIDs)
	} else {
		prompt, err = store.GetPromptBySlug(c.Context(), param, projectIDs)
	}

	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Now check for optional version or tag query parameter
	versionParam := c.Query("version")
	tagParam := c.Query("tag")

	var version *model.PromptVersion
	if versionParam != "" {
		version, err = resolveVersion(c.Context(), prompt.ID, versionParam)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.ErrVersionNotFound.Respond(c)
			}
			return apierr.ErrInternalError.Respond(c, err)
		}
	} else if tagParam != "" {
		version, err = store.GetVersionByTag(c.Context(), prompt.ID, tagParam)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.ErrVersionNotFound.Respond(c)
			}
			return apierr.ErrInternalError.Respond(c, err)
		}
	}

	resp := fiber.Map{"prompt": prompt}
	if version != nil {
		resp["version"] = version
	}

	return c.JSON(resp)
}

func ArchivePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	adminProjectIDs, err := getRequestAdminProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.ArchivePrompt(c.Context(), id, adminProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptByID(c.Context(), id, projectIDs)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

func ListAllPrompts(c *fiber.Ctx) error {
	projectIDStr := c.Query("project")
	if projectIDStr == "" {
		projectIDStr = c.Query("project_id")
	}

	if projectIDStr == "" {
		// By default nothing is shown as per backwards-compatible test requirements
		return c.JSON(fiber.Map{"prompts": []*model.Prompt{}})
	}

	projectID, err := uuid.Parse(projectIDStr)
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	allowedIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !containsUUID(allowedIDs, projectID) {
		return apierr.ErrForbidden.Respond(c)
	}

	var status *string
	statusStr := c.Query("status")
	if statusStr != "" {
		status = &statusStr
	}

	var archived *bool
	archivedStr := c.Query("archived")
	if archivedStr != "" {
		if val, err := strconv.ParseBool(archivedStr); err == nil {
			archived = &val
		}
	}

	prompts, err := store.ListPrompts(c.Context(), store.PromptFilter{
		ProjectIDs: []uuid.UUID{projectID},
		Archived:   archived,
		Status:     status,
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts})
}

type updatePromptRequest struct {
	Description string `json:"description"`
}

func UpdatePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// First verify that the prompt exists and the requester has read access
	if _, err := store.GetPromptByID(c.Context(), id, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Verify editor permissions
	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	var req updatePromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	prompt, err := store.UpdatePrompt(c.Context(), id, editorProjectIDs, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"prompt": prompt})
}

type updatePromptSchemaRequest struct {
	Schema map[string]any `json:"schema"`
}

func UpdatePromptSchema(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	var req updatePromptSchemaRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	prompt, err := store.UpdatePromptSchema(c.Context(), id, editorProjectIDs, req.Schema)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"prompt": prompt})
}

func RestorePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	adminProjectIDs, err := getRequestAdminProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.RestorePrompt(c.Context(), id, adminProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	prompt, err := store.GetPromptByID(c.Context(), id, projectIDs)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

type rollbackPromptRequest struct {
	TargetVersion int    `json:"target_version"`
	RollbackReason string `json:"rollback_reason"`
}

func RollbackPrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	var req rollbackPromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.TargetVersion <= 0 {
		return apierr.NewAPIError(fiber.StatusBadRequest, "target_version must be greater than 0").Respond(c)
	}
	if req.RollbackReason == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "rollback_reason is required").Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), id, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// We require editor capability for rollback
	prompt, err := store.GetPromptByID(c.Context(), id, editorProjectIDs)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := store.RollbackVersion(c.Context(), prompt.ID, req.TargetVersion)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return apierr.NewAPIError(fiber.StatusConflict, err.Error()).Respond(c)
		}
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": version})
}

type movePromptRequest struct {
	ProjectID string `json:"project_id"`
}

func MovePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	var req movePromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	targetProjectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid target project_id").Respond(c)
	}

	// Fetch the prompt to find its current project (basic read access first).
	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	prompt, err := store.GetPromptByID(c.Context(), id, projectIDs)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// The target project must exist.
	if _, err := store.GetProjectByID(c.Context(), targetProjectID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "target project not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Require admin capability on BOTH the source and the target project.
	adminProjectIDs, err := getRequestAdminProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !containsUUID(adminProjectIDs, prompt.ProjectID) || !containsUUID(adminProjectIDs, targetProjectID) {
		return apierr.ErrForbidden.Respond(c)
	}

	if err := store.MovePrompt(c.Context(), id, adminProjectIDs, targetProjectID); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "a prompt with this name or slug already exists in the target project").Respond(c)
		}
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Retrieve updated prompt from the target project.
	prompt, err = store.GetPromptByID(c.Context(), id, []uuid.UUID{targetProjectID})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

func DiffVersions(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	fromVal := c.Query("from")
	toVal := c.Query("to")
	if fromVal == "" || toVal == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "from and to query parameters are required").Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Verify read access to the prompt
	if _, err := store.GetPromptByID(c.Context(), id, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	fromVersion, err := resolveVersion(c.Context(), id, fromVal)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	toVersion, err := resolveVersion(c.Context(), id, toVal)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(fromVersion.Template),
		B:        difflib.SplitLines(toVersion.Template),
		FromFile: fmt.Sprintf("v%d", fromVersion.Version),
		ToFile:   fmt.Sprintf("v%d", toVersion.Version),
		Context:  3,
	}
	diffText, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{
		"from_version":  fromVersion.Version,
		"to_version":    toVersion.Version,
		"from_template": fromVersion.Template,
		"to_template":   toVersion.Template,
		"diff":          diffText,
	})
}
