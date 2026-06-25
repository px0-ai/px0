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
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

func CreateAPIKey(c *fiber.Ctx) error {
	var req createAPIKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return apierr.ErrInternalError.Respond(c)
	}
	key := "px0_" + hex.EncodeToString(raw)
	keyPrefix := key[:12] // "px0_" + first 8 hex chars
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))

	apiKey, err := store.CreateAPIKey(c.Context(), req.Name, keyPrefix, keyHash)
	if err != nil {
		return apierr.ErrInternalError.Respond(c)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         apiKey.ID,
		"name":       apiKey.Name,
		"key":        key,
		"key_prefix": apiKey.KeyPrefix,
		"created_at": apiKey.CreatedAt,
	})
}

func ListAPIKeys(c *fiber.Ctx) error {
	keys, err := store.ListAPIKeys(c.Context())
	if err != nil {
		return apierr.ErrInternalError.Respond(c)
	}
	if keys == nil {
		keys = []*model.APIKey{}
	}
	return c.JSON(fiber.Map{"api_keys": keys})
}

func DeleteAPIKey(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	if err := store.DeleteAPIKey(c.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrAPIKeyNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
