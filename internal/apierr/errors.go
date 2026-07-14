package apierr

import (
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
)

// APIError represents a standardized API error.
type APIError struct {
	Status  int    `json:"-"`
	Message string `json:"error"`
}

// Error implements the standard error interface.
func (e *APIError) Error() string {
	return e.Message
}

// Respond sends the API error as a JSON response using the configured structure.
func (e *APIError) Respond(c *fiber.Ctx, errs ...error) error {
	if len(errs) > 0 && errs[0] != nil {
		log.Printf("[ERROR] status=%d method=%s path=%s err=%v msg=%s", e.Status, c.Method(), c.Path(), errs[0], e.Message)
	} else if e.Status >= 500 {
		log.Printf("[ERROR] status=%d method=%s path=%s msg=%s", e.Status, c.Method(), c.Path(), e.Message)
	} else if e.Status >= 400 {
		log.Printf("[WARN] status=%d method=%s path=%s msg=%s", e.Status, c.Method(), c.Path(), e.Message)
	}
	return c.Status(e.Status).JSON(fiber.Map{
		"error": e.Message,
	})
}

// WithDetails creates a copy of the APIError with additional details appended to the message.
func (e *APIError) WithDetails(details string) *APIError {
	return &APIError{
		Status:  e.Status,
		Message: fmt.Sprintf("%s: %s", e.Message, details),
	}
}

// NewAPIError creates a dynamic APIError with the given status code and message.
func NewAPIError(status int, message string) *APIError {
	return &APIError{
		Status:  status,
		Message: message,
	}
}

// Predefined API Errors
var (
	ErrInvalidRequestBody       = &APIError{Status: fiber.StatusBadRequest, Message: "invalid request body"}
	ErrNameRequired             = &APIError{Status: fiber.StatusBadRequest, Message: "name is required"}
	ErrInternalError            = &APIError{Status: fiber.StatusInternalServerError, Message: "internal error. If you think this error should not have happened, please raise an issue in the GitHub repository: https://github.com/px0-ai/px0"}
	ErrInvalidID                = &APIError{Status: fiber.StatusBadRequest, Message: "invalid id"}
	ErrInvalidPromptID          = &APIError{Status: fiber.StatusBadRequest, Message: "invalid prompt id"}
	ErrAPIKeyNotFound           = &APIError{Status: fiber.StatusNotFound, Message: "api key not found"}
	ErrEmailPasswordRequired    = &APIError{Status: fiber.StatusBadRequest, Message: "email and password are required"}
	ErrPasswordTooShort         = &APIError{Status: fiber.StatusBadRequest, Message: "password must be at least 8 characters"}
	ErrInvalidEmail             = &APIError{Status: fiber.StatusBadRequest, Message: "invalid email format"}
	ErrPasswordTooWeak          = &APIError{Status: fiber.StatusBadRequest, Message: "password must contain at least one uppercase letter, one lowercase letter, one digit, and one special character"}
	ErrEmailAlreadyRegistered   = &APIError{Status: fiber.StatusConflict, Message: "email already registered"}
	ErrInvalidCredentials       = &APIError{Status: fiber.StatusUnauthorized, Message: "invalid credentials"}
	ErrAccessTokenRequired      = &APIError{Status: fiber.StatusForbidden, Message: "access token required"}
	ErrForbidden                = &APIError{Status: fiber.StatusForbidden, Message: "forbidden"}
	ErrUnauthorized             = &APIError{Status: fiber.StatusUnauthorized, Message: "unauthorized"}
	ErrPromptNotFound           = &APIError{Status: fiber.StatusNotFound, Message: "prompt not found"}
	ErrProjectNotFound          = &APIError{Status: fiber.StatusNotFound, Message: "project not found"}
	ErrPayloadNotFound          = &APIError{Status: fiber.StatusNotFound, Message: "payload not found"}
	ErrInvalidPayloadID         = &APIError{Status: fiber.StatusBadRequest, Message: "invalid payload id"}
	ErrPayloadVariablesRequired = &APIError{Status: fiber.StatusBadRequest, Message: "payload variables are required"}
	ErrInvalidPayloadVariables  = &APIError{Status: fiber.StatusBadRequest, Message: "invalid payload variables: must be valid JSON"}
	ErrNoLiveVersionFound       = &APIError{Status: fiber.StatusNotFound, Message: "no live version found for this prompt"}
	ErrInvalidVersionNumber     = &APIError{Status: fiber.StatusBadRequest, Message: "invalid version number"}
	ErrVersionNotFound          = &APIError{Status: fiber.StatusNotFound, Message: "version not found"}
	ErrTemplateParseError       = &APIError{Status: fiber.StatusInternalServerError, Message: "template parse error. If you think this error should not have happened, please raise an issue in the GitHub repository: https://github.com/px0-ai/px0"}
	ErrTemplateExecutionFailed  = &APIError{Status: fiber.StatusUnprocessableEntity, Message: "template execution failed"}
	ErrTemplateRequired         = &APIError{Status: fiber.StatusBadRequest, Message: "template is required"}
	ErrInvalidTemplate          = &APIError{Status: fiber.StatusBadRequest, Message: "invalid template"}
	ErrOnlyDraftsModifiable     = &APIError{Status: fiber.StatusUnprocessableEntity, Message: "only draft versions can be modified"}
	ErrOnlyDraftsDeletable      = &APIError{Status: fiber.StatusUnprocessableEntity, Message: "only draft versions can be deleted"}
	ErrUserNotVerified          = &APIError{Status: fiber.StatusForbidden, Message: "user is not verified"}
	ErrInvalidVerificationCode  = &APIError{Status: fiber.StatusBadRequest, Message: "invalid verification code"}
	ErrInvalidResetCode         = &APIError{Status: fiber.StatusBadRequest, Message: "invalid or expired password reset code"}
	ErrTagRequired              = &APIError{Status: fiber.StatusBadRequest, Message: "tag is required"}
	ErrInvalidTag               = &APIError{Status: fiber.StatusBadRequest, Message: "invalid tag format: must be 1-50 characters containing only letters, numbers, dashes, underscores, and dots"}
	ErrTagNotFound              = &APIError{Status: fiber.StatusNotFound, Message: "tag not found"}

	// Skill Registry Errors
	ErrSkillNotFound            = &APIError{Status: fiber.StatusNotFound, Message: "skill not found"}
	ErrInvalidSkillID           = &APIError{Status: fiber.StatusBadRequest, Message: "invalid skill id"}
	ErrFileNotFound             = &APIError{Status: fiber.StatusNotFound, Message: "file not found"}
	ErrFilePathRequired         = &APIError{Status: fiber.StatusBadRequest, Message: "file path is required"}
	ErrFileAlreadyExists        = &APIError{Status: fiber.StatusConflict, Message: "file already exists"}
)
