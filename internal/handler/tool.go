package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

// Helper to resolve tool by ID or slug and check viewer access.
func resolveTool(c *fiber.Ctx) (*model.Tool, error) {
	param := c.Params("id")
	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return nil, fmt.Errorf("viewer projects: %w", err)
	}

	var tool *model.Tool
	id, err := uuid.Parse(param)
	if err == nil {
		tool, err = store.GetToolByID(c.Context(), id, projectIDs)
	} else {
		tool, err = store.GetToolBySlug(c.Context(), param, projectIDs)
	}

	if err != nil {
		return nil, err
	}
	return tool, nil
}

// Helper to check if user has editor permission on tool's project.
func checkToolEditor(c *fiber.Ctx, tool *model.Tool) (bool, error) {
	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return false, err
	}
	for _, pid := range editorProjectIDs {
		if pid == tool.ProjectID {
			return true, nil
		}
	}
	return false, nil
}

type createToolRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// CreateTool creates a tool under a project (requires editor role).
func CreateTool(c *fiber.Ctx) error {
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

	var req createToolRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	slugStr := req.Slug
	if slugStr == "" {
		slugStr = req.Name
	}
	slugStr = NormalizeSlug(slugStr)

	tool, err := store.CreateTool(c.Context(), projectID, slugStr, req.Name, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "tool with this name or slug already exists in project").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"tool": tool})
}

// ListTools lists all tools in a project (requires viewer role).
func ListTools(c *fiber.Ctx) error {
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

	tools, err := store.ListTools(c.Context(), []uuid.UUID{projectID})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if tools == nil {
		tools = []*model.Tool{}
	}
	return c.JSON(fiber.Map{"tools": tools})
}

// ListAllTools lists tools globally, filtered by an explicit project ID query parameter.
func ListAllTools(c *fiber.Ctx) error {
	projectIDStr := c.Query("project")
	if projectIDStr == "" {
		projectIDStr = c.Query("project_id")
	}

	if projectIDStr == "" {
		// By default nothing is shown, similar to ListAllPrompts
		return c.JSON(fiber.Map{"tools": []*model.Tool{}})
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

	tools, err := store.ListTools(c.Context(), []uuid.UUID{projectID})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if tools == nil {
		tools = []*model.Tool{}
	}
	return c.JSON(fiber.Map{"tools": tools})
}

// GetTool returns details of a specific tool.
func GetTool(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"tool": tool})
}

type updateToolRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// UpdateTool updates tool details (requires editor role).
func UpdateTool(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	var req updateToolRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	slugStr := req.Slug
	if slugStr == "" {
		slugStr = req.Name
	}
	slugStr = NormalizeSlug(slugStr)

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	updated, err := store.UpdateTool(c.Context(), tool.ID, editorProjectIDs, slugStr, req.Name, req.Description)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "tool with this name or slug already exists in project").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"tool": updated})
}

// DeleteTool deletes a tool (requires editor role).
func DeleteTool(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.DeleteTool(c.Context(), tool.ID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

type createToolVersionRequest struct {
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

// CreateToolVersion creates a new tool version in 'draft' status (requires editor role).
func CreateToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	var req createToolVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if len(req.InputSchema) == 0 {
		req.InputSchema = json.RawMessage("{}")
	}
	if len(req.OutputSchema) == 0 {
		req.OutputSchema = json.RawMessage("{}")
	}

	v, err := store.CreateToolVersion(c.Context(), tool.ID, req.InputSchema, req.OutputSchema)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": v})
}

// ListToolVersions lists all versions of a tool.
func ListToolVersions(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	versions, err := store.ListToolVersions(c.Context(), tool.ID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if versions == nil {
		versions = []*model.ToolVersion{}
	}
	return c.JSON(fiber.Map{"versions": versions})
}

func parseVersionParam(param string) (int, error) {
	vNum, err := strconv.Atoi(param)
	if err != nil || vNum <= 0 {
		return 0, errors.New("invalid version number")
	}
	return vNum, nil
}

// GetToolVersion gets details of a specific version of a tool.
func GetToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	vNum, err := parseVersionParam(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.GetToolVersion(c.Context(), tool.ID, vNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

type updateToolVersionRequest struct {
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

// UpdateToolVersion updates a draft version of a tool (requires editor role).
func UpdateToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	vNum, err := parseVersionParam(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	currentVersion, err := store.GetToolVersion(c.Context(), tool.ID, vNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if currentVersion.Status != "draft" {
		return apierr.ErrOnlyDraftsModifiable.Respond(c)
	}

	var req updateToolVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if len(req.InputSchema) == 0 {
		req.InputSchema = currentVersion.InputSchema
	}
	if len(req.OutputSchema) == 0 {
		req.OutputSchema = currentVersion.OutputSchema
	}

	v, err := store.UpdateToolVersion(c.Context(), currentVersion.ID, req.InputSchema, req.OutputSchema)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// DeleteToolVersion deletes/archives a specific version of a tool (requires editor role).
func DeleteToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	vNum, err := parseVersionParam(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	if err := store.DeleteToolVersion(c.Context(), tool.ID, vNum); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// PromoteToolVersion promotes version: draft -> stable -> live (requires editor role).
func PromoteToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	vNum, err := parseVersionParam(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.PromoteToolVersion(c.Context(), tool.ID, vNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// DemoteToolVersion demotes version: live -> stable (requires editor role).
func DemoteToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	vNum, err := parseVersionParam(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.DemoteToolVersion(c.Context(), tool.ID, vNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// ArchiveToolVersion archives a specific version of a tool (requires editor role).
func ArchiveToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	vNum, err := parseVersionParam(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.ArchiveToolVersion(c.Context(), tool.ID, vNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"version": v})
}

// DuplicateToolVersion duplicates an existing tool version into a new draft version (requires editor role).
func DuplicateToolVersion(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	isEditor, err := checkToolEditor(c, tool)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isEditor {
		return apierr.ErrForbidden.Respond(c)
	}

	vNum, err := parseVersionParam(c.Params("version"))
	if err != nil {
		return apierr.ErrInvalidVersionNumber.Respond(c)
	}

	v, err := store.DuplicateToolVersion(c.Context(), tool.ID, vNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": v})
}
