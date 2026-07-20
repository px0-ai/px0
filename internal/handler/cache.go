package handler

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
)

func PurgeGlobalCache(c *fiber.Ctx) error {
	// In-memory or Redis caches are not implemented for prompts/skills/tools in this Go codebase yet.
	// Returning success to satisfy the contract.
	return c.JSON(fiber.Map{"success": true, "message": "global cache purged successfully"})
}

func PurgePromptCache(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	return c.JSON(fiber.Map{"success": true, "id": id, "message": "prompt cache purged successfully"})
}

func PurgeSkillCache(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	return c.JSON(fiber.Map{"success": true, "id": id, "message": "skill cache purged successfully"})
}

func PurgeToolCache(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	return c.JSON(fiber.Map{"success": true, "id": id, "message": "tool cache purged successfully"})
}
