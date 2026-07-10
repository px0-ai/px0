package handler

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

// isProjectEditor reports whether the requester may create projects owned by the
// given team: an editor-or-above member of the team, or an admin of its org.
func isProjectEditor(c *fiber.Ctx, team *model.Team) (bool, error) {
	isEditor, err := IsTeamEditor(c, team.ID)
	if err != nil {
		return false, err
	}
	if isEditor {
		return true, nil
	}
	if team.OrgID != nil {
		return IsOrgAdmin(c, *team.OrgID)
	}
	return false, nil
}

// isProjectAdmin reports whether the requester may administer the given team's
// projects (delete, grant, revoke): an admin of the team, or an admin of its org.
func isProjectAdmin(c *fiber.Ctx, team *model.Team) (bool, error) {
	isAdmin, err := IsTeamAdmin(c, team.ID)
	if err != nil {
		return false, err
	}
	if isAdmin {
		return true, nil
	}
	if team.OrgID != nil {
		return IsOrgAdmin(c, *team.OrgID)
	}
	return false, nil
}

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

	allowed, err := isProjectEditor(c, team)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
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
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apierr.ErrInvalidID.Respond(c)
	}

	teamIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	accessible, err := store.IsProjectAccessibleByTeams(c.Context(), id, teamIDs)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !accessible {
		return apierr.ErrProjectNotFound.Respond(c)
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

	allowedIDs, err := getRequestTeamIDs(c)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	isMember := false
	for _, tid := range allowedIDs {
		if tid == teamID {
			isMember = true
			break
		}
	}
	if !isMember {
		return apierr.ErrForbidden.Respond(c)
	}

	projects, err := store.ListProjectsForTeam(c.Context(), teamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if projects == nil {
		projects = []*model.Project{}
	}
	return c.JSON(fiber.Map{"projects": projects})
}

func DeleteProject(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
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

	team, err := store.GetTeamByID(c.Context(), project.OwningTeamID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	allowed, err := isProjectAdmin(c, team)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}
	if !allowed {
		return apierr.ErrForbidden.Respond(c)
	}

	if err := store.DeleteProject(c.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrProjectNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
