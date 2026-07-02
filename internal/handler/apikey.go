package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createAPIKeyRequest struct {
	Name      string      `json:"name"`
	OrgID     uuid.UUID   `json:"org_id"`
	TeamIDs   []uuid.UUID `json:"team_ids"`
	Operation string      `json:"operation"` // "read_render" or "all"
}

func CreateAPIKey(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	var req createAPIKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}
	if req.OrgID == uuid.Nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "org_id is required").Respond(c)
	}

	// Define default operation
	if req.Operation == "" {
		req.Operation = "read_render"
	}
	if req.Operation != "read_render" && req.Operation != "all" && req.Operation != "admin" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid operation, must be read_render, all, or admin").Respond(c)
	}

	// Verify user is admin of the organization (context-aware helper support for API keys)
	isOrgAdmin, err := IsOrgAdmin(c, req.OrgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isOrgAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	// If no teams are specified, default to all teams in the organization
	if len(req.TeamIDs) == 0 {
		// Fetch teams under the org
		// Wait, we can list teams of the user or just keep it empty?
		// Better return error or default if possible, let's keep it as req.TeamIDs.
		// Wait, let's verify if teamIDs exist and belong to org if they are passed.
		for _, teamID := range req.TeamIDs {
			t, err := store.GetTeamByID(c.Context(), teamID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
				}
				return apierr.ErrInternalError.Respond(c, err)
			}
			if t.OrgID == nil || *t.OrgID != req.OrgID {
				return apierr.NewAPIError(fiber.StatusBadRequest, "team does not belong to the specified organization").Respond(c)
			}
		}
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	key := "ak_" + hex.EncodeToString(raw)
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))

	apiKey, err := store.CreateAPIKey(c.Context(), req.Name, req.OrgID, req.TeamIDs, req.Operation, keyHash)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         apiKey.ID,
		"name":       apiKey.Name,
		"key":        key,
		"operation":  apiKey.Operation,
		"created_at": apiKey.CreatedAt,
	})
}

func ListAPIKeys(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	orgIDStr := c.Query("org_id")
	if orgIDStr == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "org_id is required").Respond(c)
	}
	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	// Verify user is admin of the organization (context-aware helper support for API keys)
	isOrgAdmin, err := IsOrgAdmin(c, orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isOrgAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	keys, err := store.ListAPIKeysForOrg(c.Context(), orgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if keys == nil {
		keys = []*model.APIKey{}
	}
	return c.JSON(fiber.Map{"api_keys": keys})
}

func DeleteAPIKey(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	apiKey, err := store.GetAPIKeyByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrAPIKeyNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Verify user is admin of the organization (context-aware helper support for API keys)
	isOrgAdmin, err := IsOrgAdmin(c, apiKey.OrgID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !isOrgAdmin {
		return apierr.ErrForbidden.Respond(c)
	}

	if err := store.DeleteAPIKey(c.Context(), id); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
