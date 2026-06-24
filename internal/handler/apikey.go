package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/arpitbhayani/px0/internal/model"
	"github.com/arpitbhayani/px0/internal/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

func CreateAPIKey(c *fiber.Ctx) error {
	var req createAPIKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	key := "px0_" + hex.EncodeToString(raw)
	keyPrefix := key[:12] // "px0_" + first 8 hex chars
	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))

	apiKey, err := store.CreateAPIKey(c.Context(), req.Name, keyPrefix, keyHash)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":          apiKey.ID,
		"name":        apiKey.Name,
		"key":         key,
		"key_prefix":  apiKey.KeyPrefix,
		"created_at":  apiKey.CreatedAt,
	})
}

func ListAPIKeys(c *fiber.Ctx) error {
	keys, err := store.ListAPIKeys(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if keys == nil {
		keys = []*model.APIKey{}
	}
	return c.JSON(fiber.Map{"api_keys": keys})
}

func DeleteAPIKey(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}

	if err := store.DeleteAPIKey(c.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "api key not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}
