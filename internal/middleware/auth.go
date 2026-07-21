package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

const LocalsUserID = "userID"

// RequireAuth accepts a standard access token (Authorization: Bearer <token>)
// which can be either a user session token (sess_...) or an API Key (ak_...).
func RequireAuth(c *fiber.Ctx) error {
	if tryAccessTokenAuth(c) {
		subjVal := c.Locals("subject")
		if subjVal != nil {
			subj := subjVal.(*model.Subject)
			if !subj.IsAPIKey && !subj.IsUserVerified {
				return apierr.ErrUserNotVerified.Respond(c)
			}
			return c.Next()
		}
	}
	return apierr.ErrUnauthorized.Respond(c)
}

// RequireAccessToken accepts either a user session access token or an API key with 'all' or 'admin' operation.
func RequireAccessToken(c *fiber.Ctx) error {
	if tryAccessTokenAuth(c) {
		subjVal := c.Locals("subject")
		if subjVal != nil {
			subj := subjVal.(*model.Subject)
			if !subj.IsAPIKey {
				if !subj.IsUserVerified {
					return apierr.ErrUserNotVerified.Respond(c)
				}
				return c.Next()
			}

			// For API Key: must have some Editor/Admin capability or IsOrgAdmin
			hasAccess := subj.IsOrgAdmin
			if !hasAccess {
				for _, role := range subj.TeamRoles {
					if role == model.RoleEditor || role == model.RoleAdmin {
						hasAccess = true
						break
					}
				}
			}
			if !hasAccess {
				return apierr.ErrForbidden.Respond(c)
			}
			return c.Next()
		}
	}
	return apierr.ErrUnauthorized.Respond(c)
}

// RequireSessionToken accepts only standard user session tokens (not API Keys).
func RequireSessionToken(c *fiber.Ctx) error {
	if tryAccessTokenAuth(c) {
		subjVal := c.Locals("subject")
		if subjVal != nil {
			subj := subjVal.(*model.Subject)
			if subj.IsAPIKey {
				return apierr.ErrUnauthorized.Respond(c)
			}
			if !subj.IsUserVerified {
				return apierr.ErrUserNotVerified.Respond(c)
			}
			return c.Next()
		}
	}
	return apierr.ErrUnauthorized.Respond(c)
}

// RequireAdmin accepts an API key with 'all'/'admin' or a system admin user.
func RequireAdmin(c *fiber.Ctx) error {
	if tryAccessTokenAuth(c) {
		subjVal := c.Locals("subject")
		if subjVal != nil {
			subj := subjVal.(*model.Subject)
			if subj.IsAPIKey {
				// API Key admin equivalent
				hasAccess := subj.IsOrgAdmin
				if !hasAccess {
					for _, role := range subj.TeamRoles {
						if role == model.RoleEditor || role == model.RoleAdmin {
							hasAccess = true
							break
						}
					}
				}
				if !hasAccess {
					return apierr.ErrForbidden.Respond(c)
				}
				return c.Next()
			}

			// User session
			if !subj.IsUserVerified {
				return apierr.ErrUserNotVerified.Respond(c)
			}
			if !subj.IsUserAdmin {
				return apierr.ErrForbidden.Respond(c)
			}
			return c.Next()
		}
	}
	return apierr.ErrUnauthorized.Respond(c)
}

func tryAccessTokenAuth(c *fiber.Ctx) bool {
	auth := c.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return false
	}

	if strings.HasPrefix(token, "ak_") {
		return tryAPIKeyWithRawKey(c, token)
	}

	if !strings.HasPrefix(token, "sess_") {
		return false
	}

	session, err := store.GetSessionByToken(c.Context(), token)
	if err != nil {
		return false
	}

	user, err := store.GetUserByID(c.Context(), session.UserID)
	if err != nil {
		return false
	}

	teamRoles, err := store.GetUserTeamRoles(c.Context(), user.ID)
	if err != nil {
		return false
	}

	var orgID *uuid.UUID
	isOrgAdmin := false

	// Compute OrgID and IsOrgAdmin
	teams, err := store.GetUserTeams(c.Context(), user.ID)
	if err == nil && len(teams) > 0 {
		// Use the first team's org as primary, if it exists
		for _, t := range teams {
			if t.OrgID != nil {
				orgID = t.OrgID
				admin, _ := store.IsOrgAdmin(c.Context(), user.ID, *orgID)
				isOrgAdmin = admin
				break
			}
		}
	}

	subj := &model.Subject{
		UserID:         user.ID,
		IsUserAdmin:    user.IsAdmin,
		IsUserVerified: user.IsVerified,
		IsAPIKey:       false,
		OrgID:          orgID,
		IsOrgAdmin:     isOrgAdmin,
		TeamRoles:      teamRoles,
	}

	c.Locals("subject", subj)
	c.Locals(LocalsUserID, user.ID)
	c.Locals("currentUser", user)
	return true
}

func tryAPIKeyWithRawKey(c *fiber.Ctx, key string) bool {
	if !strings.HasPrefix(key, "ak_") {
		return false
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	apiKey, err := store.GetAPIKeyByHash(c.Context(), hash)
	if err != nil {
		return false
	}

	// Update last_used_at without blocking the request.
	go func() {
		_ = store.TouchAPIKey(context.Background(), apiKey.ID)
	}()

	teamIDs, err := store.GetAPIKeyTeams(c.Context(), apiKey.ID)
	if err != nil {
		return false
	}

	if len(teamIDs) == 0 {
		// Global access key: Load all teams in this organization
		teams, err := store.GetTeamsByOrgID(c.Context(), apiKey.OrgID)
		if err != nil {
			return false
		}
		for _, t := range teams {
			teamIDs = append(teamIDs, t.ID)
		}
	}

	role := model.RoleViewer
	if apiKey.Operation == "all" {
		role = model.RoleEditor
	} else if apiKey.Operation == "admin" {
		role = model.RoleAdmin
	}

	teamRoles := make(map[uuid.UUID]string)
	for _, id := range teamIDs {
		teamRoles[id] = role
	}

	userID := uuid.Nil
	if apiKey.Operation == "admin" || apiKey.Operation == "all" {
		adminUserID, err := store.GetOrgAdminUserID(c.Context(), apiKey.OrgID)
		if err == nil {
			userID = adminUserID
		}
	}

	subj := &model.Subject{
		UserID:      userID,
		IsUserAdmin: false, // API Keys are not system admins
		IsAPIKey:    true,
		OrgID:       &apiKey.OrgID,
		IsOrgAdmin:  apiKey.Operation == "admin",
		TeamRoles:   teamRoles,
	}

	c.Locals("subject", subj)
	c.Locals(LocalsUserID, userID)
	return true
}
