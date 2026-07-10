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
	id, err := uuid.Parse(c.Params("projectID"))
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

type grantProjectAccessRequest struct {
	TeamID string `json:"team_id"`
}

// requireProjectAdmin loads the project and its owning team and verifies the
// requester is an admin of the owning team (or its org). When it returns
// ok == false it has already written the appropriate error response to c, and
// the caller should return nil.
func requireProjectAdmin(c *fiber.Ctx, projectID uuid.UUID) (project *model.Project, team *model.Team, ok bool) {
	project, err := store.GetProjectByID(c.Context(), projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_ = apierr.ErrProjectNotFound.Respond(c)
		} else {
			_ = apierr.ErrInternalError.Respond(c, err)
		}
		return nil, nil, false
	}

	team, err = store.GetTeamByID(c.Context(), project.OwningTeamID)
	if err != nil {
		_ = apierr.ErrInternalError.Respond(c, err)
		return nil, nil, false
	}

	allowed, err := isProjectAdmin(c, team)
	if err != nil {
		_ = apierr.ErrInternalError.Respond(c, err)
		return nil, nil, false
	}
	if !allowed {
		_ = apierr.ErrForbidden.Respond(c)
		return nil, nil, false
	}
	return project, team, true
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

	project, owningTeam, ok := requireProjectAdmin(c, projectID)
	if !ok {
		return nil
	}

	// Sharing is bounded to the owning team's organization; an org-less team
	// may hold a private project but cannot share it.
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

	if _, _, ok := requireProjectAdmin(c, projectID); !ok {
		return nil
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

	if _, _, ok := requireProjectAdmin(c, id); !ok {
		return nil
	}

	if err := store.DeleteProject(c.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrProjectNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
