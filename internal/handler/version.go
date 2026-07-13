package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"text/template"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createVersionRequest struct {
	Template    string          `json:"template"`
	Model       *string         `json:"model"`
	ModelParams json.RawMessage `json:"model_params"`
}

type updateVersionRequest struct {
	Template    *string         `json:"template"`
	Model       *string         `json:"model"`
	ModelParams json.RawMessage `json:"model_params"`

	HasModel       bool `json:"-"`
	HasModelParams bool `json:"-"`
}

func (r *updateVersionRequest) UnmarshalJSON(data []byte) error {
	type Alias updateVersionRequest
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*r = updateVersionRequest(aux)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if _, ok := raw["model"]; ok {
		r.HasModel = true
	}
	if _, ok := raw["model_params"]; ok {
		r.HasModelParams = true
	}
	return nil
}

func CreateVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var req createVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Template == "" {
		return apierr.ErrTemplateRequired.Respond(c)
	}
	if _, err := template.New("").Parse(req.Template); err != nil {
		return apierr.ErrInvalidTemplate.WithDetails(err.Error()).Respond(c, err)
	}
	versionModel, err := normalizeVersionModelPtr(req.Model)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, err.Error()).Respond(c)
	}

	if err := validateModelParams(req.ModelParams); err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, err.Error()).Respond(c)
	}

	version, err := store.CreateVersion(c.Context(), promptID, store.CreateVersionParams{
		Template:    req.Template,
		Model:       versionModel,
		ModelParams: req.ModelParams,
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": version})
}

func ListVersions(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
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

	var status *string
	statusStr := c.Query("status")
	if statusStr != "" {
		status = &statusStr
	}

	versions, err := store.ListVersions(c.Context(), promptID, store.VersionFilter{
		Status: status,
		Tags:   tags,
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if versions == nil {
		versions = []*model.PromptVersion{}
	}
	return c.JSON(fiber.Map{"versions": versions})
}

func GetVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": version})
}

func UpdateVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	existing, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	if existing.Status != model.VersionStatusDraft {
		return apierr.ErrOnlyDraftsModifiable.Respond(c)
	}

	var req updateVersionRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Template == nil && !req.HasModel && !req.HasModelParams {
		return apierr.NewAPIError(fiber.StatusBadRequest, "template, model, or model_params is required").Respond(c)
	}
	if req.Template != nil {
		if *req.Template == "" {
			return apierr.ErrTemplateRequired.Respond(c)
		}
		if _, err := template.New("").Parse(*req.Template); err != nil {
			return apierr.ErrInvalidTemplate.WithDetails(err.Error()).Respond(c, err)
		}
	}
	var versionModel *string
	if req.HasModel {
		versionModel, err = normalizeVersionModelPtr(req.Model)
		if err != nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, err.Error()).Respond(c)
		}
	}

	if req.HasModelParams {
		if err := validateModelParams(req.ModelParams); err != nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, err.Error()).Respond(c)
		}
	}

	updated, err := store.UpdateVersion(c.Context(), existing.ID, store.UpdateVersionParams{
		Template:          req.Template,
		Model:             versionModel,
		UpdateModel:       req.HasModel,
		ModelParams:       req.ModelParams,
		UpdateModelParams: req.HasModelParams,
	})
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": updated})
}

func PromoteVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	target, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := store.PromoteVersion(c.Context(), promptID, target.Version)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.NewAPIError(fiber.StatusUnprocessableEntity, err.Error()).Respond(c, err)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": version})
}

func DemoteVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	target, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := store.DemoteVersion(c.Context(), promptID, target.Version)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.NewAPIError(fiber.StatusUnprocessableEntity, err.Error()).Respond(c, err)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": version})
}

func ArchiveVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	target, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := store.ArchiveVersion(c.Context(), promptID, target.Version)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.NewAPIError(fiber.StatusUnprocessableEntity, err.Error()).Respond(c, err)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"version": version})
}

func DeleteVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	target, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	err = store.DeleteVersion(c.Context(), promptID, target.Version)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		if errors.Is(err, store.ErrConflict) {
			return apierr.ErrOnlyDraftsDeletable.Respond(c, err)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func DuplicateVersion(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	projectIDs, err := getRequestViewerProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, projectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	editorProjectIDs, err := getRequestEditorProjectIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if _, err := store.GetPromptByID(c.Context(), promptID, editorProjectIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrForbidden.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	target, err := resolveVersion(c.Context(), promptID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	version, err := store.DuplicateVersion(c.Context(), promptID, target.Version)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"version": version})
}

func resolveVersion(ctx context.Context, promptID uuid.UUID, versionParam string) (*model.PromptVersion, error) {
	if versionNum, err := strconv.Atoi(versionParam); err == nil {
		return store.GetVersion(ctx, promptID, versionNum)
	}
	return store.GetVersionByTag(ctx, promptID, versionParam)
}

func normalizeVersionModelPtr(modelName *string) (*string, error) {
	if modelName == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*modelName)
	if trimmed == "" {
		return nil, errors.New("model must not be empty")
	}
	return &trimmed, nil
}

func validateModelParams(params json.RawMessage) error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	var object map[string]any
	if err := json.Unmarshal(params, &object); err != nil || object == nil {
		return errors.New("model_params must be a JSON object")
	}
	return nil
}
