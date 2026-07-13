package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type createProjectRequest struct {
	TeamID string `json:"team_id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
}

func CreateProject(c *fiber.Ctx) error {
	var req createProjectRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	teamID, err := uuid.Parse(req.TeamID)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid team_id").Respond(c)
	}
	if req.Name == "" {
		return apierr.ErrNameRequired.Respond(c)
	}

	team, err := store.GetTeamByID(c.Context(), teamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	subj, err := getRequestSubject(c)
	if err != nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	allowed := false
	if subj.IsUserAdmin {
		allowed = true
	} else if subj.IsOrgAdmin && team.OrgID != nil && subj.OrgID != nil && *team.OrgID == *subj.OrgID {
		allowed = true
	} else {
		if role, ok := subj.TeamRoles[teamID]; ok && hasRequiredRole(role, model.RoleEditor) {
			allowed = true
		}
	}

	if !allowed {
		return apierr.ErrForbidden.Respond(c)
	}

	slug := req.Slug
	if slug == "" {
		slug = req.Name
	}
	slug = NormalizeSlug(slug)

	project, err := store.CreateProject(c.Context(), teamID, slug, req.Name)
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.NewAPIError(fiber.StatusConflict, "project with this name or slug already exists in the team; please provide a unique name").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"project": project})
}

func GetProject(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	project, err := store.GetProjectByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrProjectNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.JSON(fiber.Map{"project": project})
}

func ListTeamProjects(c *fiber.Ctx) error {
	teamID, err := uuid.Parse(c.Params("teamID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	// Will be protected by RequireTeamRole(model.RoleViewer) on the route

	projects, err := store.ListProjectsForTeam(c.Context(), teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if projects == nil {
		projects = []*model.Project{}
	}
	return c.JSON(fiber.Map{"projects": projects})
}

type grantProjectAccessRequest struct {
	TeamID string `json:"team_id"`
}

func GrantProjectAccess(c *fiber.Ctx) error {
	projectID, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	var req grantProjectAccessRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	granteeID, err := uuid.Parse(req.TeamID)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, "invalid team_id").Respond(c)
	}

	project, err := store.GetProjectByID(c.Context(), projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrProjectNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	owningTeam, err := store.GetTeamByID(c.Context(), project.OwningTeamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if owningTeam.OrgID == nil {
		return apierr.NewAPIError(fiber.StatusForbidden, "the project's owning team has no organization; sharing is not available").Respond(c)
	}

	if granteeID == project.OwningTeamID {
		return apierr.NewAPIError(fiber.StatusBadRequest, "the owning team already has implicit access").Respond(c)
	}

	grantee, err := store.GetTeamByID(c.Context(), granteeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	if grantee.OrgID == nil || *grantee.OrgID != *owningTeam.OrgID {
		return apierr.NewAPIError(fiber.StatusForbidden, "grantee team must belong to the owning team's organization").Respond(c)
	}

	if err := store.GrantProjectAccess(c.Context(), projectID, granteeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrProjectNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"access": fiber.Map{"project_id": projectID, "team_id": granteeID},
	})
}

func RevokeProjectAccess(c *fiber.Ctx) error {
	projectID, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}
	granteeID, err := uuid.Parse(c.Params("teamID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	if err := store.RevokeProjectAccess(c.Context(), projectID, granteeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "access grant not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func DeleteProject(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("projectID"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	if err := store.DeleteProject(c.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrProjectNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
