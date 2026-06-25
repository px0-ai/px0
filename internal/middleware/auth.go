package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/store"
)

const LocalsUserID = "userID"

// RequireAuth accepts either a session token (Authorization: Bearer <token>)
// or an API key (X-API-Key: <key>).
func RequireAuth(c *fiber.Ctx) error {
	if trySessionAuth(c) || tryAPIKeyAuth(c) {
		return c.Next()
	}
	return apierr.ErrUnauthorized.Respond(c)
}

// RequireSession accepts only a session token. Used for endpoints that manage
// account-level resources (e.g. API key CRUD) where API key auth is not appropriate.
func RequireSession(c *fiber.Ctx) error {
	if trySessionAuth(c) {
		return c.Next()
	}
	return apierr.ErrUnauthorized.Respond(c)
}

func RequireAdmin(c *fiber.Ctx) error {
	if !trySessionAuth(c) {
		return apierr.ErrUnauthorized.Respond(c)
	}

	userID, ok := c.Locals(LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return apierr.ErrUnauthorized.Respond(c)
	}

	user, err := store.GetUserByID(c.Context(), userID)
	if err != nil || !user.IsAdmin {
		return apierr.ErrForbidden.Respond(c)
	}
	return c.Next()
}

func trySessionAuth(c *fiber.Ctx) bool {
	auth := c.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return false
	}

	session, err := store.GetSessionByToken(c.Context(), token)
	if err != nil {
		return false
	}

	c.Locals(LocalsUserID, session.UserID)
	return true
}

func tryAPIKeyAuth(c *fiber.Ctx) bool {
	key := c.Get("X-API-Key")
	if key == "" {
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

	c.Locals("apiKeyTeamID", apiKey.TeamID)
	// API key auth has no associated user; use uuid.Nil as sentinel.
	c.Locals(LocalsUserID, uuid.Nil)
	return true
}
