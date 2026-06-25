package app

import (
	"github.com/gofiber/fiber/v2"

	"github.com/arpitbhayani/px0/internal/handler"
	"github.com/arpitbhayani/px0/internal/middleware"
)

func New() *fiber.App {
	app := fiber.New(fiber.Config{AppName: "px0"})

	app.Use(middleware.Metrics())

	v1 := app.Group("/v1")
	v1.Get("/health", handler.Hello)

	auth := v1.Group("/auth")
	auth.Post("/register", handler.Register)
	auth.Post("/login", handler.Login)
	auth.Delete("/session", handler.Logout)
	auth.Get("/me", middleware.RequireSession, handler.Me)

	apiKeys := v1.Group("/api-keys", middleware.RequireSession)
	apiKeys.Post("", handler.CreateAPIKey)
	apiKeys.Get("", handler.ListAPIKeys)
	apiKeys.Delete("/:id", handler.DeleteAPIKey)

	prompts := v1.Group("/prompts", middleware.RequireAuth)
	prompts.Post("", handler.CreatePrompt)
	prompts.Get("", handler.ListPrompts)
	prompts.Get("/:id", handler.GetPrompt)
	prompts.Delete("/:id", handler.DeletePrompt)

	prompts.Post("/:id/render", handler.RenderLive)

	prompts.Post("/:id/versions", handler.CreateVersion)
	prompts.Get("/:id/versions", handler.ListVersions)
	prompts.Get("/:id/versions/:version", handler.GetVersion)
	prompts.Put("/:id/versions/:version", handler.UpdateVersion)
	prompts.Post("/:id/versions/:version/publish", handler.PublishVersion)
	prompts.Post("/:id/versions/:version/render", handler.RenderVersion)

	return app
}
