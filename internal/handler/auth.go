package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/arpitbhayani/px0/internal/middleware"
	"github.com/arpitbhayani/px0/internal/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and password are required"})
	}
	if len(req.Password) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "password must be at least 8 characters"})
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	user, err := store.CreateUser(c.Context(), req.Email, string(hash))
	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "email already registered"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"user": user})
}

func Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	user, err := store.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
	}

	token, err := generateToken()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	hours := 24
	if h, err := strconv.Atoi(os.Getenv("SESSION_DURATION_HOURS")); err == nil && h > 0 {
		hours = h
	}

	expiresAt := time.Now().Add(time.Duration(hours) * time.Hour)
	session, err := store.CreateSession(c.Context(), user.ID, token, expiresAt)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	return c.JSON(fiber.Map{
		"token":      session.Token,
		"expires_at": session.ExpiresAt,
		"user":       user,
	})
}

func Logout(c *fiber.Ctx) error {
	auth := c.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token != "" {
		_ = store.DeleteSession(c.Context(), token)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func Me(c *fiber.Ctx) error {
	userID, ok := c.Locals(middleware.LocalsUserID).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "session required"})
	}

	user, err := store.GetUserByID(c.Context(), userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	return c.JSON(fiber.Map{"user": user})
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
