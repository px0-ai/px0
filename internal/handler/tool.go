package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/getkin/kin-openapi/openapi3"
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
	URL         string `json:"url"`
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

	if req.URL != "" {
		if _, err := url.ParseRequestURI(req.URL); err != nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, "invalid tool url format").Respond(c)
		}
	}

	slugStr := req.Slug
	if slugStr == "" {
		slugStr = req.Name
	}
	slugStr = NormalizeSlug(slugStr)

	tool, err := store.CreateTool(c.Context(), projectID, slugStr, req.Name, req.Description, req.URL)
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
	URL         string `json:"url"`
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

	if req.URL != "" {
		if _, err := url.ParseRequestURI(req.URL); err != nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, "invalid tool url format").Respond(c)
		}
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

	updated, err := store.UpdateTool(c.Context(), tool.ID, editorProjectIDs, slugStr, req.Name, req.Description, req.URL)
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

func ValidateJSONSchema(schemaBytes []byte, dataBytes []byte) error {
	var schema openapi3.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return fmt.Errorf("invalid json schema: %w", err)
	}
	var value any
	if err := json.Unmarshal(dataBytes, &value); err != nil {
		return fmt.Errorf("invalid json payload: %w", err)
	}
	if err := schema.VisitJSON(value); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	return nil
}

// InvokeLive executes the active 'live' version of the tool.
func InvokeLive(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := store.GetLiveToolVersion(c.Context(), tool.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "no live version found for this tool").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return executeInvoke(c, tool, version)
}

// InvokeVersion executes a specific version of the tool.
func InvokeVersion(c *fiber.Ctx) error {
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

	version, err := store.GetToolVersion(c.Context(), tool.ID, vNum)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return executeInvoke(c, tool, version)
}

func executeInvoke(c *fiber.Ctx, tool *model.Tool, version *model.ToolVersion) error {
	var payload json.RawMessage
	bodyBytes := c.Body()
	if len(bodyBytes) > 0 {
		payload = json.RawMessage(bodyBytes)
	} else {
		payload = json.RawMessage("{}")
	}

	// 1. Validate request payload against input schema (if non-empty)
	if len(version.InputSchema) > 0 && string(version.InputSchema) != "{}" && string(version.InputSchema) != "null" {
		if err := ValidateJSONSchema(version.InputSchema, payload); err != nil {
			errStr := fmt.Sprintf("Request validation failed: %v", err)
			statusCode := http.StatusBadRequest
			if _, logErr := store.LogToolInvocation(c.Context(), tool.ID, version.Version, payload, nil, &errStr, &statusCode); logErr != nil {
				return apierr.ErrInternalError.Respond(c, logErr)
			}
			return apierr.NewAPIError(fiber.StatusBadRequest, errStr).Respond(c)
		}
	}

	// 2. Assert tool URL is configured
	if tool.URL == "" {
		errStr := "tool URL is not configured"
		statusCode := http.StatusBadRequest
		if _, logErr := store.LogToolInvocation(c.Context(), tool.ID, version.Version, payload, nil, &errStr, &statusCode); logErr != nil {
			return apierr.ErrInternalError.Respond(c, logErr)
		}
		return apierr.NewAPIError(fiber.StatusBadRequest, errStr).Respond(c)
	}

	// 3. Make the HTTP POST call to the tool URL with payload
	httpReq, err := http.NewRequestWithContext(c.Context(), "POST", tool.URL, bytes.NewReader(payload))
	if err != nil {
		errStr := fmt.Sprintf("failed to create HTTP request: %v", err)
		statusCode := http.StatusInternalServerError
		if _, logErr := store.LogToolInvocation(c.Context(), tool.ID, version.Version, payload, nil, &errStr, &statusCode); logErr != nil {
			return apierr.ErrInternalError.Respond(c, logErr)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		errStr := fmt.Sprintf("failed to execute tool call: %v", err)
		statusCode := http.StatusBadGateway
		if _, logErr := store.LogToolInvocation(c.Context(), tool.ID, version.Version, payload, nil, &errStr, &statusCode); logErr != nil {
			return apierr.ErrInternalError.Respond(c, logErr)
		}
		return apierr.NewAPIError(fiber.StatusBadGateway, errStr).Respond(c, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		errStr := fmt.Sprintf("failed to read response body: %v", err)
		statusCode := resp.StatusCode
		if _, logErr := store.LogToolInvocation(c.Context(), tool.ID, version.Version, payload, nil, &errStr, &statusCode); logErr != nil {
			return apierr.ErrInternalError.Respond(c, logErr)
		}
		return apierr.NewAPIError(fiber.StatusBadGateway, errStr).Respond(c, err)
	}

	var respRaw json.RawMessage = respBody
	if len(respRaw) == 0 {
		respRaw = json.RawMessage("null")
	}

	// 4. Validate response against output schema (if non-empty)
	if len(version.OutputSchema) > 0 && string(version.OutputSchema) != "{}" && string(version.OutputSchema) != "null" {
		if err := ValidateJSONSchema(version.OutputSchema, respRaw); err != nil {
			errStr := fmt.Sprintf("Response validation failed: %v", err)
			statusCode := resp.StatusCode
			if _, logErr := store.LogToolInvocation(c.Context(), tool.ID, version.Version, payload, &respRaw, &errStr, &statusCode); logErr != nil {
				return apierr.ErrInternalError.Respond(c, logErr)
			}
			return apierr.NewAPIError(fiber.StatusUnprocessableEntity, errStr).Respond(c)
		}
	}

	// 5. Log the successful execution
	statusCode := resp.StatusCode
	if _, logErr := store.LogToolInvocation(c.Context(), tool.ID, version.Version, payload, &respRaw, nil, &statusCode); logErr != nil {
		return apierr.ErrInternalError.Respond(c, logErr)
	}

	var parsedResponse any
	if err := json.Unmarshal(respRaw, &parsedResponse); err != nil {
		parsedResponse = string(respRaw)
	}

	return c.JSON(fiber.Map{
		"status_code": resp.StatusCode,
		"response":    parsedResponse,
	})
}

// ListToolInvocations returns a paginated history of tool executions/invocations using cursor-based pagination.
func ListToolInvocations(c *fiber.Ctx) error {
	tool, err := resolveTool(c)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrToolNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	cursorStr := c.Query("cursor")
	var cursor int64
	if cursorStr != "" {
		cursor, err = strconv.ParseInt(cursorStr, 10, 64)
		if err != nil || cursor <= 0 {
			return apierr.NewAPIError(fiber.StatusBadRequest, "invalid cursor").Respond(c)
		}
	}

	limitStr := c.Query("limit")
	limit := 10
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 {
			return apierr.NewAPIError(fiber.StatusBadRequest, "invalid limit").Respond(c)
		}
	}

	invocations, err := store.ListToolInvocations(c.Context(), tool.ID, cursor, limit)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if invocations == nil {
		invocations = []*model.ToolInvocation{}
	}

	var nextCursor string
	if len(invocations) > 0 {
		nextCursor = strconv.FormatInt(invocations[len(invocations)-1].ID, 10)
	}

	return c.JSON(fiber.Map{
		"invocations": invocations,
		"next_cursor": nextCursor,
	})
}
