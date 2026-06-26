package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/middleware"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type registerRequest struct {
	Email    string     `json:"email"`
	Password string     `json:"password"`
	TeamID   *uuid.UUID `json:"team_id,omitempty"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Email == "" || req.Password == "" {
		return apierr.ErrEmailPasswordRequired.Respond(c)
	}
	if len(req.Password) < 8 {
		return apierr.ErrPasswordTooShort.Respond(c)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Determine if the caller is an Admin (using ADMIN_KEY or a valid admin session)
	isCallerAdmin := false
	adminKey := os.Getenv("ADMIN_KEY")
	authHeader := c.Get("Authorization")
	apiKeyHeader := c.Get("X-API-Key")
	token := strings.TrimPrefix(authHeader, "Bearer ")

	if adminKey != "" && (token == adminKey || authHeader == adminKey || apiKeyHeader == adminKey) {
		isCallerAdmin = true
	} else if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") && token != "" {
		if session, err := store.GetSessionByToken(c.Context(), token); err == nil {
			if callerUser, err := store.GetUserByID(c.Context(), session.UserID); err == nil && callerUser.IsAdmin {
				isCallerAdmin = true
			}
		}
	}

	// Validate team_id depending on whether the caller is an Admin
	if isCallerAdmin {
		if req.TeamID == nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, "admin must pass team_id when registering a user").Respond(c)
		}
	} else {
		if req.TeamID != nil {
			return apierr.NewAPIError(fiber.StatusForbidden, "only admins can register users with a team_id").Respond(c)
		}
	}

	var user *model.User
	if isCallerAdmin {
		// Admin registers standard user directly as verified
		user, err = store.CreateUser(c.Context(), req.Email, string(hash))
		if err == nil {
			// Automatically mark user as verified
			_ = store.VerifyUser(c.Context(), user.ID)
			user.IsVerified = true

			// Join specified team
			if err = store.AddTeamMember(c.Context(), *req.TeamID, user.ID); err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
		}
	} else {
		// Public registration creates a new Admin user (unverified by default)
		user, err = store.CreateAdminUser(c.Context(), req.Email, string(hash), false)
	}

	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.ErrEmailAlreadyRegistered.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !isCallerAdmin {
		code, err := GenerateVerificationCode()
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}

		expiresAt := time.Now().Add(15 * time.Minute)
		if err := store.CreateUserVerification(c.Context(), user.ID, code, expiresAt); err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}

		if err := SendVerificationEmail(user.Email, code); err != nil {
			_ = apierr.NewAPIError(fiber.StatusInternalServerError, "failed to send verification email").Respond(c, err)
			return nil
		}
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"user": user})
}

func Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	user, err := store.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		return apierr.ErrInvalidCredentials.Respond(c)
	}

	if !user.IsVerified {
		return apierr.ErrUserNotVerified.Respond(c)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return apierr.ErrInvalidCredentials.Respond(c)
	}

	token, err := generateToken()
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	hours := 24
	if h, err := strconv.Atoi(os.Getenv("SESSION_DURATION_HOURS")); err == nil && h > 0 {
		hours = h
	}

	expiresAt := time.Now().Add(time.Duration(hours) * time.Hour)
	session, err := store.CreateSession(c.Context(), user.ID, token, expiresAt)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	return c.JSON(fiber.Map{
		"token":      session.Token,
		"expires_at": session.ExpiresAt,
		"user":       user,
	})
}

func Verify(c *fiber.Ctx) error {
	var req verifyRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.Email == "" || req.Code == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "email and code are required").Respond(c)
	}

	user, err := store.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		return apierr.ErrInvalidCredentials.Respond(c)
	}

	if user.IsVerified {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"message": "user is already verified"})
	}

	code, expiresAt, err := store.GetLatestVerificationCode(c.Context(), user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrInvalidVerificationCode.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if req.Code != code || time.Now().After(expiresAt) {
		return apierr.ErrInvalidVerificationCode.Respond(c)
	}

	if err := store.VerifyUser(c.Context(), user.ID); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	_ = store.DeleteUserVerifications(c.Context(), user.ID)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"message": "email verified successfully"})
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
		return apierr.ErrSessionRequired.Respond(c)
	}

	user, err := store.GetUserByID(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
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

func GenerateVerificationCode() (string, error) {
	var code string
	for i := 0; i < 6; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		code += num.String()
	}
	return code, nil
}

func SendVerificationEmail(email, code string) error {
	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" {
		fmt.Printf("[EMAIL] No RESEND_API_KEY set. Verification code for %s is: %s\n", email, code)
		return nil
	}

	from := os.Getenv("RESEND_FROM_EMAIL")
	if from == "" {
		from = "onboarding@resend.dev"
	}

	payload := map[string]any{
		"from":    from,
		"to":      []string{email},
		"subject": "Verify your email address",
		"html":    fmt.Sprintf("<p>Your verification code is: <strong>%s</strong></p>", code),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("resend returned status: %d", resp.StatusCode)
	}

	return nil
}
