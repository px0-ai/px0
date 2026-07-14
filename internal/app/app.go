package app

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/px0-ai/px0/internal/handler"
	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/model"
)

func New() *fiber.App {
	app := fiber.New(fiber.Config{AppName: "px0"})

	app.Use(cors.New(cors.Config{
		AllowOrigins:     "http://localhost:3001, http://127.0.0.1:3001",
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization, X-API-Key",
		AllowMethods:     "GET, POST, HEAD, PUT, DELETE, PATCH, OPTIONS",
		AllowCredentials: true,
	}))

	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
	}))
	app.Use(middleware.Metrics())

	v1 := app.Group("/v1")
	v1.Get("/health", handler.Health)

	auth := v1.Group("/auth")
	auth.Post("/register", handler.Register)
	auth.Post("/login", handler.Login)
	auth.Get("/verify-email", handler.TriggerVerification)
	auth.Post("/verify-email", handler.Verify)
	auth.Post("/password-reset/trigger", handler.TriggerPasswordReset)
	auth.Post("/password-reset/reset", handler.ResetPassword)
	auth.Delete("/session", handler.Logout)
	auth.Get("/me", middleware.RequireAccessToken, handler.Me)
	auth.Put("/me", middleware.RequireAccessToken, handler.UpdateMe)
	auth.Post("/me/change-password", middleware.RequireAccessToken, handler.ChangePassword)
	auth.Delete("/me", middleware.RequireAccessToken, handler.DeleteMe)

	me := v1.Group("/me", middleware.RequireAccessToken)
	me.Get("/teams", handler.ListUserTeams)
	me.Get("/orgs", handler.ListUserOrgs)
	me.Get("/inbox", handler.GetAdminInbox)
	me.Delete("/teams/:teamID", handler.LeaveTeam)

	orgs := v1.Group("/orgs", middleware.RequireAccessToken)
	orgs.Post("", handler.CreateOrg)
	orgs.Put("/:id", handler.UpdateOrg)
	orgs.Delete("/:id", handler.DeleteOrg)
	orgs.Post("/:orgID/teams", handler.CreateTeam)
	orgs.Get("/:orgID/teams", handler.ListOrgTeams)
	orgs.Get("/:orgID/people", handler.ListOrgPeople)
	orgs.Delete("/:orgID/members/:userID", handler.RemoveOrgMember)

	projects := v1.Group("/projects", middleware.RequireAuth)
	projects.Post("", handler.CreateProject)
	projects.Get("/:projectID", middleware.RequireProjectRole(model.RoleViewer), handler.GetProject)
	projects.Delete("/:projectID", middleware.RequireProjectRole(model.RoleAdmin), handler.DeleteProject)
	projects.Post("/:projectID/access", middleware.RequireProjectRole(model.RoleAdmin), handler.GrantProjectAccess)
	projects.Delete("/:projectID/access/:teamID", middleware.RequireProjectRole(model.RoleAdmin), handler.RevokeProjectAccess)
	projects.Post("/:projectID/prompts", middleware.RequireProjectRole(model.RoleEditor), handler.CreatePrompt)
	projects.Get("/:projectID/prompts", middleware.RequireProjectRole(model.RoleViewer), handler.ListPrompts)
	projects.Post("/:projectID/prompts/:slug/render", middleware.RequireProjectRole(model.RoleViewer), handler.RenderLive)
	projects.Post("/:projectID/prompts/:slug/versions/:version/render", middleware.RequireProjectRole(model.RoleViewer), handler.RenderVersion)
	projects.Post("/:projectID/skills", middleware.RequireProjectRole(model.RoleEditor), handler.CreateSkill)
	projects.Get("/:projectID/skills", middleware.RequireProjectRole(model.RoleViewer), handler.ListSkills)

	teams := v1.Group("/teams", middleware.RequireAccessToken)
	teams.Get("/:teamID/projects", middleware.RequireTeamRole(model.RoleViewer), handler.ListTeamProjects)
	teams.Put("/:id", handler.UpdateTeam)
	teams.Post("/:id/members", handler.AddTeamMember)
	teams.Delete("/:id/members/:userID", handler.RemoveTeamMember)
	teams.Get("/:id/members", handler.ListTeamMembers)
	teams.Put("/:id/members/:userID/role", handler.UpdateTeamMemberRole)
	teams.Post("/:id/join-requests", handler.CreateJoinRequest)
	teams.Delete("/:id", handler.DeleteTeam)

	joinRequests := v1.Group("/join-requests", middleware.RequireAccessToken)
	joinRequests.Put("/:id", handler.ResolveJoinRequest)

	apiKeys := v1.Group("/api-keys", middleware.RequireSessionToken)
	apiKeys.Post("", handler.CreateAPIKey)
	apiKeys.Get("", handler.ListAPIKeys)
	apiKeys.Delete("/:id", handler.DeleteAPIKey)

	prompts := v1.Group("/prompts", middleware.RequireAuth)
	prompts.Get("", handler.ListAllPrompts)
	prompts.Get("/:id", handler.GetPrompt)
	prompts.Put("/:id", handler.UpdatePrompt)
	prompts.Post("/:id/archive", handler.ArchivePrompt)
	prompts.Post("/:id/restore", handler.RestorePrompt)
	prompts.Post("/:id/move", handler.MovePrompt)
	prompts.Get("/:id/versions/diff", handler.DiffVersions)

	prompts.Post("/:id/versions", handler.CreateVersion)
	prompts.Get("/:id/versions", handler.ListVersions)
	prompts.Get("/:id/versions/:version", handler.GetVersion)
	prompts.Put("/:id/versions/:version", handler.UpdateVersion)
	prompts.Delete("/:id/versions/:version", handler.DeleteVersion)
	prompts.Post("/:id/versions/:version/promote", handler.PromoteVersion)
	prompts.Post("/:id/versions/:version/demote", handler.DemoteVersion)
	prompts.Post("/:id/versions/:version/archive", handler.ArchiveVersion)
	prompts.Post("/:id/versions/:version/duplicate", handler.DuplicateVersion)
	prompts.Post("/:id/versions/:version/tags", handler.SetTag)
	prompts.Delete("/:id/tags/:tag", handler.RemoveTag)
	prompts.Get("/:id/tags", handler.ListTags)

	prompts.Post("/:id/payloads", handler.CreatePromptPayload)
	prompts.Get("/:id/payloads", handler.ListPromptPayloads)
	prompts.Get("/:id/payloads/:payloadID", handler.GetPromptPayload)
	prompts.Put("/:id/payloads/:payloadID", handler.UpdatePromptPayload)
	prompts.Delete("/:id/payloads/:payloadID", handler.DeletePromptPayload)

	skills := v1.Group("/skills", middleware.RequireAuth)
	skills.Get("/:id", handler.GetSkill)
	skills.Put("/:id", handler.UpdateSkill)
	skills.Delete("/:id", handler.DeleteSkill)
	skills.Post("/:id/versions", handler.CreateSkillVersion)
	skills.Get("/:id/versions", handler.ListSkillVersions)
	skills.Get("/:id/versions/:version", handler.GetSkillVersion)
	skills.Delete("/:id/versions/:version", handler.DeleteSkillVersion)
	skills.Post("/:id/versions/:version/promote", handler.PromoteSkillVersion)
	skills.Post("/:id/versions/:version/demote", handler.DemoteSkillVersion)
	skills.Post("/:id/versions/:version/archive", handler.ArchiveSkillVersion)
	skills.Post("/:id/versions/:version/duplicate", handler.DuplicateSkillVersion)
	skills.Post("/:id/versions/:version/upload", handler.UploadSkillZip)
	skills.Get("/:id/versions/:version/download", handler.DownloadSkillZip)
	skills.Get("/:id/versions/:version/files", handler.ListSkillFiles)
	skills.Get("/:id/versions/:version/files/content", handler.GetSkillFileContent)
	skills.Post("/:id/versions/:version/files", handler.UpsertSkillFile)
	skills.Put("/:id/versions/:version/files", handler.UpsertSkillFile)
	skills.Delete("/:id/versions/:version/files", handler.DeleteSkillFile)

	return app
}
