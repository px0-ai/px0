package handler

import (
	"encoding/json"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createPromptPayloadRequest struct {
	Variables json.RawMessage `json:"variables"`
}

type updatePromptPayloadRequest struct {
	Name      *string         `json:"name"`
	Variables json.RawMessage `json:"variables"`
}

func CreatePromptPayload(c *fiber.Ctx) error {
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

	var req createPromptPayloadRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if len(req.Variables) == 0 {
		return apierr.ErrPayloadVariablesRequired.Respond(c)
	}

	if !json.Valid(req.Variables) {
		return apierr.ErrInvalidPayloadVariables.Respond(c)
	}

	payload, err := store.CreatePromptPayload(c.Context(), promptID, req.Variables)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"payload": payload})
}

func GetPromptPayload(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	payloadID, err := uuid.Parse(c.Params("payloadID"))
	if err != nil {
		return apierr.ErrInvalidPayloadID.Respond(c)
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

	payload, err := store.GetPromptPayload(c.Context(), payloadID, promptID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPayloadNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"payload": payload})
}

func ListPromptPayloads(c *fiber.Ctx) error {
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

	payloads, err := store.ListPromptPayloads(c.Context(), promptID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if payloads == nil {
		payloads = []*model.PromptPayload{} // empty slice
	}

	// Wait, store package returns []*model.PromptPayload. Let's make sure type is resolved correctly.
	// We can write []*model.PromptPayload but store package imports model. Let's use fiber.Map.
	return c.JSON(fiber.Map{"payloads": payloads})
}

func UpdatePromptPayload(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	payloadID, err := uuid.Parse(c.Params("payloadID"))
	if err != nil {
		return apierr.ErrInvalidPayloadID.Respond(c)
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

	var req updatePromptPayloadRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if len(req.Variables) > 0 && !json.Valid(req.Variables) {
		return apierr.ErrInvalidPayloadVariables.Respond(c)
	}

	payload, err := store.UpdatePromptPayload(c.Context(), payloadID, promptID, req.Name, req.Variables)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPayloadNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{"payload": payload})
}

func DeletePromptPayload(c *fiber.Ctx) error {
	promptID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	payloadID, err := uuid.Parse(c.Params("payloadID"))
	if err != nil {
		return apierr.ErrInvalidPayloadID.Respond(c)
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

	if err := store.DeletePromptPayload(c.Context(), payloadID, promptID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPayloadNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.SendStatus(fiber.StatusNoContent)
}
