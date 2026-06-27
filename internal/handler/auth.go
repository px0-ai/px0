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
	"regexp"
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

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

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

func isValidPassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, char := range password {
		switch {
		case char >= 'A' && char <= 'Z':
			hasUpper = true
		case char >= 'a' && char <= 'z':
			hasLower = true
		case char >= '0' && char <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	return hasUpper && hasLower && hasDigit && hasSpecial
}

func Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}
	if req.Email == "" || req.Password == "" {
		return apierr.ErrEmailPasswordRequired.Respond(c)
	}
	if !emailRegex.MatchString(req.Email) {
		return apierr.ErrInvalidEmail.Respond(c)
	}
	if len(req.Password) < 8 {
		return apierr.ErrPasswordTooShort.Respond(c)
	}
	if !isValidPassword(req.Password) {
		return apierr.ErrPasswordTooWeak.Respond(c)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	// Determine if the caller is an Admin (using a valid admin access token)
	isCallerAdmin := false
	var callerUser *model.User

	authHeader := c.Get("Authorization")
	if authHeader != "" {
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return apierr.ErrUnauthorized.Respond(c)
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			return apierr.ErrUnauthorized.Respond(c)
		}

		session, err := store.GetSessionByToken(c.Context(), token)
		if err != nil {
			return apierr.ErrUnauthorized.Respond(c)
		}

		u, err := store.GetUserByID(c.Context(), session.UserID)
		if err != nil {
			return apierr.ErrUnauthorized.Respond(c)
		}

		if !u.IsVerified {
			return apierr.ErrUserNotVerified.Respond(c)
		}

		if !u.IsAdmin {
			return apierr.ErrForbidden.Respond(c)
		}

		isCallerAdmin = true
		callerUser = u
	}

	// Validate team_id depending on whether the caller is an Admin
	if isCallerAdmin {
		if req.TeamID == nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, "admin must pass team_id when registering a user").Respond(c)
		}

		// Check team belongs to org and user is admin of that org/belongs to it
		team, err := store.GetTeamByID(c.Context(), *req.TeamID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.NewAPIError(fiber.StatusNotFound, "team not found").Respond(c)
			}
			return apierr.ErrInternalError.Respond(c, err)
		}

		if team.OrgID == nil {
			return apierr.NewAPIError(fiber.StatusBadRequest, "team does not belong to any organization").Respond(c)
		}

		callerTeams, err := store.GetUserTeams(c.Context(), callerUser.ID)
		if err != nil {
			return apierr.ErrInternalError.Respond(c, err)
		}

		belongsToOrg := false
		for _, t := range callerTeams {
			if t.OrgID != nil && *t.OrgID == *team.OrgID {
				belongsToOrg = true
				break
			}
		}

		if !belongsToOrg {
			return apierr.NewAPIError(fiber.StatusForbidden, "user does not belong to the organization of the specified team").Respond(c)
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
		// Public registration creates a new Admin user (unverified by default unless RESEND_API_KEY == "mock")
		autoVerify := os.Getenv("RESEND_API_KEY") == "mock"
		user, err = store.CreateAdminUser(c.Context(), req.Email, string(hash), autoVerify)
		if err == nil {
			// Automatically create Default Org and Default Team, making user ADMIN of the org
			org, err := store.CreateOrganization(c.Context(), "Default Org")
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}

			team, err := store.CreateTeamWithOrg(c.Context(), "Default Team", org.ID)
			if err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}

			if err = store.AddTeamMember(c.Context(), team.ID, user.ID); err != nil {
				return apierr.ErrInternalError.Respond(c, err)
			}
		}
	}

	if err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return apierr.ErrEmailAlreadyRegistered.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if !isCallerAdmin {
		autoVerify := os.Getenv("RESEND_API_KEY") == "mock"
		if autoVerify {
			return c.Status(fiber.StatusCreated).JSON(fiber.Map{"user": user})
		}

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

func TriggerVerification(c *fiber.Ctx) error {
	email := c.Query("email")
	if email == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "email is required").Respond(c)
	}

	user, err := store.GetUserByEmail(c.Context(), email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "user not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if user.IsVerified {
		return apierr.NewAPIError(fiber.StatusBadRequest, "user is already verified").Respond(c)
	}

	code, err := GenerateVerificationCode()
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	expiresAt := time.Now().Add(15 * time.Minute)
	if err := store.CreateUserVerification(c.Context(), user.ID, code, expiresAt); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := SendVerificationEmail(user.Email, code); err != nil {
		return apierr.NewAPIError(fiber.StatusInternalServerError, "failed to send verification email").Respond(c, err)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "verification email sent successfully",
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
		return apierr.ErrAccessTokenRequired.Respond(c)
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
	return "sess_" + hex.EncodeToString(b), nil
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
	if apiKey == "" || apiKey == "mock" {
		fmt.Printf("[EMAIL] No RESEND_API_KEY set or is mock. Verification code for %s is: %s\n", email, code)
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

type triggerPasswordResetRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

func SendPasswordResetEmail(email, code string) error {
	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" || apiKey == "mock" {
		fmt.Printf("[EMAIL] No RESEND_API_KEY set or is mock. Password reset code for %s is: %s\n", email, code)
		return nil
	}

	from := os.Getenv("RESEND_FROM_EMAIL")
	if from == "" {
		from = "onboarding@resend.dev"
	}

	payload := map[string]any{
		"from":    from,
		"to":      []string{email},
		"subject": "Reset your password",
		"html":    fmt.Sprintf("<p>Your password reset code is: <strong>%s</strong></p>", code),
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

func TriggerPasswordReset(c *fiber.Ctx) error {
	var req triggerPasswordResetRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.Email == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "email is required").Respond(c)
	}

	user, err := store.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.NewAPIError(fiber.StatusNotFound, "user not found").Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	code, err := GenerateVerificationCode()
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	expiresAt := time.Now().Add(15 * time.Minute)
	_ = store.DeleteUserPasswordResets(c.Context(), user.ID)
	if err := store.CreatePasswordReset(c.Context(), user.ID, code, expiresAt); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := SendPasswordResetEmail(user.Email, code); err != nil {
		return apierr.NewAPIError(fiber.StatusInternalServerError, "failed to send password reset email").Respond(c, err)
	}

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "password reset email sent successfully",
	})
}

func ResetPassword(c *fiber.Ctx) error {
	var req resetPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	if req.Code == "" || req.NewPassword == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "code and new_password are required").Respond(c)
	}

	if len(req.NewPassword) < 8 {
		return apierr.ErrPasswordTooShort.Respond(c)
	}

	if !isValidPassword(req.NewPassword) {
		return apierr.ErrPasswordTooWeak.Respond(c)
	}

	userID, expiresAt, err := store.GetPasswordResetByCode(c.Context(), req.Code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrInvalidResetCode.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	if time.Now().After(expiresAt) {
		_ = store.DeletePasswordReset(c.Context(), req.Code)
		return apierr.ErrInvalidResetCode.Respond(c)
	}

	user, err := store.GetUserByID(c.Context(), userID)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	if err := store.UpdateUserPassword(c.Context(), user.ID, string(hash)); err != nil {
		return apierr.ErrInternalError.Respond(c, err)
	}

	_ = store.DeletePasswordReset(c.Context(), req.Code)

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "password reset successfully",
		"email":   user.Email,
	})
}
