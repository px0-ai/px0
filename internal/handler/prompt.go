package handler

import (
	"errors"

	"github.com/arpitbhayani/px0/internal/model"
	"github.com/arpitbhayani/px0/internal/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type createPromptRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func CreatePrompt(c *fiber.Ctx) error {
	var req createPromptRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}

	prompt, err := store.CreatePrompt(c.Context(), req.Name, req.Description)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"prompt": prompt})
}

func ListPrompts(c *fiber.Ctx) error {
	prompts, err := store.ListPrompts(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts})
}

func GetPrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid prompt id"})
	}

	prompt, err := store.GetPromptByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "prompt not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

func DeletePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid prompt id"})
	}

	if err := store.DeletePrompt(c.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "prompt not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}
