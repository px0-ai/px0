package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

// hasRequiredRole checks if the actual role meets or exceeds minRole.
func hasRequiredRole(actualRole, minRole string) bool {
	if actualRole == model.RoleAdmin {
		return true
	}
	if actualRole == model.RoleEditor && (minRole == model.RoleEditor || minRole == model.RoleViewer) {
		return true
	}
	if actualRole == model.RoleViewer && minRole == model.RoleViewer {
		return true
	}
	return false
}

// RequireOrgAdmin requires the subject to be a system admin or an org admin.
func RequireOrgAdmin() fiber.Handler {
	return func(c *fiber.Ctx) error {
		subjVal := c.Locals("subject")
		if subjVal == nil {
			return apierr.ErrUnauthorized.Respond(c)
		}
		subj := subjVal.(*model.Subject)

		if subj.IsUserAdmin || subj.IsOrgAdmin {
			return c.Next()
		}
		return apierr.ErrForbidden.Respond(c)
	}
}

// RequireTeamRole checks if the subject has at least the minRole on the team specified in the path.
func RequireTeamRole(minRole string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		subjVal := c.Locals("subject")
		if subjVal == nil {
			return apierr.ErrUnauthorized.Respond(c)
		}
		subj := subjVal.(*model.Subject)

		teamIDStr := c.Params("teamID")
		if teamIDStr == "" {
			teamIDStr = c.Params("id")
		}
		teamID, err := uuid.Parse(teamIDStr)
		if err != nil {
			return apierr.ErrInvalidID.Respond(c)
		}

		if subj.IsUserAdmin {
			return c.Next()
		}

		// Org Admin check: must belong to the same Org
		if subj.IsOrgAdmin && subj.OrgID != nil {
			t, err := store.GetTeamByID(c.Context(), teamID)
			if err == nil && t.OrgID != nil && *t.OrgID == *subj.OrgID {
				return c.Next()
			}
		}

		role, ok := subj.TeamRoles[teamID]
		if !ok || !hasRequiredRole(role, minRole) {
			return apierr.ErrForbidden.Respond(c)
		}

		return c.Next()
	}
}

// RequireProjectRole checks if the subject has at least the minRole on the project's owning team or any granted team.
func RequireProjectRole(minRole string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		subjVal := c.Locals("subject")
		if subjVal == nil {
			return apierr.ErrUnauthorized.Respond(c)
		}
		subj := subjVal.(*model.Subject)

		projectIDStr := c.Params("projectID")
		if projectIDStr == "" {
			projectIDStr = c.Params("id")
		}
		projectID, err := uuid.Parse(projectIDStr)
		if err != nil {
			return apierr.ErrInvalidID.Respond(c)
		}

		if subj.IsUserAdmin {
			return c.Next()
		}

		project, err := store.GetProjectByID(c.Context(), projectID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.ErrProjectNotFound.Respond(c)
			}
			return apierr.ErrInternalError.Respond(c, err)
		}

		// Org Admin check: must belong to the same Org as the project owning team
		if subj.IsOrgAdmin && subj.OrgID != nil {
			t, err := store.GetTeamByID(c.Context(), project.OwningTeamID)
			if err == nil && t.OrgID != nil && *t.OrgID == *subj.OrgID {
				return c.Next()
			}
		}

		// Gather all teams where the subject has at least viewer role to check reachability
		var viewerTeams []uuid.UUID
		for tID, r := range subj.TeamRoles {
			if hasRequiredRole(r, model.RoleViewer) {
				viewerTeams = append(viewerTeams, tID)
			}
		}

		if len(viewerTeams) == 0 {
			return apierr.ErrProjectNotFound.Respond(c)
		}

		viewerAccessible, err := store.IsProjectAccessibleByTeams(c.Context(), projectID, viewerTeams)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}
		if !viewerAccessible {
			return apierr.ErrProjectNotFound.Respond(c)
		}

		// Now check if they have the actual requested minRole
		if minRole != model.RoleViewer {
			var qualifyingTeams []uuid.UUID
			for tID, r := range subj.TeamRoles {
				if hasRequiredRole(r, minRole) {
					qualifyingTeams = append(qualifyingTeams, tID)
				}
			}

			if len(qualifyingTeams) == 0 {
				return apierr.ErrForbidden.Respond(c)
			}

			accessible, err := store.IsProjectAccessibleByTeams(c.Context(), projectID, qualifyingTeams)
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
			if !accessible {
				return apierr.ErrForbidden.Respond(c)
			}
		}

		return c.Next()
	}
}
