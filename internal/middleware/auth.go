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
		userVal := c.Locals("currentUser")
		if userVal != nil {
			user := userVal.(*model.User)
			if !user.IsVerified {
				return apierr.ErrUserNotVerified.Respond(c)
			}
		}
		return c.Next()
	}
	return apierr.ErrUnauthorized.Respond(c)
}

// RequireAccessToken accepts either a user session access token or an API key with 'all' or 'admin' operation.
func RequireAccessToken(c *fiber.Ctx) error {
	if tryAccessTokenAuth(c) {
		userVal := c.Locals("currentUser")
		if userVal != nil {
			user := userVal.(*model.User)
			if !user.IsVerified {
				return apierr.ErrUserNotVerified.Respond(c)
			}
		}
		if operation, ok := c.Locals("apiKeyOperation").(string); ok {
			if operation != "all" && operation != "admin" {
				return apierr.ErrForbidden.Respond(c)
			}
		}
		return c.Next()
	}
	return apierr.ErrUnauthorized.Respond(c)
}

// RequireSessionToken accepts only standard user session tokens (not API Keys).
func RequireSessionToken(c *fiber.Ctx) error {
	if tryAccessTokenAuth(c) {
		// Reject if authenticated via an API Key
		if _, isAPIKey := c.Locals("apiKeyOperation").(string); isAPIKey {
			return apierr.ErrUnauthorized.Respond(c)
		}

		userID, ok := c.Locals(LocalsUserID).(uuid.UUID)
		if ok && userID != uuid.Nil {
			userVal := c.Locals("currentUser")
			if userVal != nil {
				user := userVal.(*model.User)
				if !user.IsVerified {
					return apierr.ErrUserNotVerified.Respond(c)
				}
			}
			return c.Next()
		}
	}
	return apierr.ErrUnauthorized.Respond(c)
}

func RequireAdmin(c *fiber.Ctx) error {
	if tryAccessTokenAuth(c) {
		// Is it an API Key?
		if operation, ok := c.Locals("apiKeyOperation").(string); ok {
			if operation != "all" && operation != "admin" {
				return apierr.ErrForbidden.Respond(c)
			}
			return c.Next()
		}

		// Otherwise standard user session
		userID, ok := c.Locals(LocalsUserID).(uuid.UUID)
		if !ok || userID == uuid.Nil {
			return apierr.ErrUnauthorized.Respond(c)
		}

		userVal := c.Locals("currentUser")
		if userVal == nil {
			return apierr.ErrUnauthorized.Respond(c)
		}
		user := userVal.(*model.User)
		if !user.IsVerified {
			return apierr.ErrUserNotVerified.Respond(c)
		}
		if !user.IsAdmin {
			return apierr.ErrForbidden.Respond(c)
		}
		return c.Next()
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

	// Fetch user
	user, err := store.GetUserByID(c.Context(), session.UserID)
	if err != nil {
		return false
	}

	c.Locals(LocalsUserID, session.UserID)
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

	// For admin or all scope API keys, resolve the actual Org Admin user ID of that organization
	// to allow seamless execution of downstream handlers that require a logged-in user.
	userID := uuid.Nil
	if apiKey.Operation == "admin" || apiKey.Operation == "all" {
		adminUserID, err := store.GetOrgAdminUserID(c.Context(), apiKey.OrgID)
		if err == nil {
			userID = adminUserID
		}
	}

	c.Locals("apiKeyTeamIDs", teamIDs)
	c.Locals("apiKeyOrgID", apiKey.OrgID)
	c.Locals("apiKeyOperation", apiKey.Operation)
	c.Locals(LocalsUserID, userID)
	return true
}
