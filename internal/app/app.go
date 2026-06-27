package app

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/px0-ai/px0/internal/handler"
	"github.com/px0-ai/px0/internal/middleware"
)

func New() *fiber.App {
	app := fiber.New(fiber.Config{AppName: "px0"})

	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
	}))
	app.Use(middleware.Metrics())

	v1 := app.Group("/v1")
	v1.Get("/health", handler.Health)

	auth := v1.Group("/auth")
	auth.Post("/register", handler.Register)
	auth.Post("/login", handler.Login)
	auth.Post("/verify", handler.Verify)
	auth.Delete("/session", handler.Logout)
	auth.Get("/me", middleware.RequireAccessToken, handler.Me)

	me := v1.Group("/me", middleware.RequireAccessToken)
	me.Get("/teams", handler.ListUserTeams)

	orgs := v1.Group("/orgs", middleware.RequireAdmin)
	orgs.Post("", handler.CreateOrg)
	orgs.Put("/:id", handler.UpdateOrg)
	orgs.Post("/:orgID/teams", handler.CreateTeam)

	teamPrompts := v1.Group("/teams/:teamID/prompts", middleware.RequireAuth)
	teamPrompts.Post("", handler.CreatePrompt)
	teamPrompts.Get("", handler.ListPrompts)

	teams := v1.Group("/teams", middleware.RequireAdmin)
	teams.Put("/:id", handler.UpdateTeam)
	teams.Post("/:id/members", handler.AddTeamMember)
	teams.Delete("/:id/members/:userID", handler.RemoveTeamMember)

	apiKeys := v1.Group("/api-keys", middleware.RequireAccessToken)
	apiKeys.Post("", handler.CreateAPIKey)
	apiKeys.Get("", handler.ListAPIKeys)
	apiKeys.Delete("/:id", handler.DeleteAPIKey)

	prompts := v1.Group("/prompts", middleware.RequireAuth)
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
