package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createPromptRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func CreatePrompt(c *fiber.Ctx) error {
	var req createPromptRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	prompt, err := store.CreatePrompt(c.Context(), req.Name, req.Description)
	if err != nil {
		return apierr.ErrInternalError.Respond(c)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"prompt": prompt})
}

func ListPrompts(c *fiber.Ctx) error {
	prompts, err := store.ListPrompts(c.Context())
	if err != nil {
		return apierr.ErrInternalError.Respond(c)
	}
	if prompts == nil {
		prompts = []*model.Prompt{}
	}
	return c.JSON(fiber.Map{"prompts": prompts})
}

func GetPrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	prompt, err := store.GetPromptByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c)
	}
	return c.JSON(fiber.Map{"prompt": prompt})
}

func DeletePrompt(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidPromptID.Respond(c)
	}

	if err := store.DeletePrompt(c.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrPromptNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
