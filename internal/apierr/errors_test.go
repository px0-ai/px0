package apierr_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arpitbhayani/px0/internal/apierr"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

func TestAPIError(t *testing.T) {
	err := apierr.ErrInvalidRequestBody
	assert.Equal(t, "invalid request body", err.Error())

	detailed := err.WithDetails("field 'email' is missing")
	assert.Equal(t, fiber.StatusBadRequest, detailed.Status)
	assert.Equal(t, "invalid request body: field 'email' is missing", detailed.Error())

	dyn := apierr.NewAPIError(fiber.StatusConflict, "custom conflict")
	assert.Equal(t, fiber.StatusConflict, dyn.Status)
	assert.Equal(t, "custom conflict", dyn.Error())
}

func TestAPIError_Respond(t *testing.T) {
	app := fiber.New()
	app.Get("/test-err", func(c *fiber.Ctx) error {
		return apierr.ErrInvalidRequestBody.Respond(c)
	})
	app.Get("/test-details", func(c *fiber.Ctx) error {
		return apierr.ErrInvalidTemplate.WithDetails("bad template syntax").Respond(c)
	})

	// Test static error
	req := httptest.NewRequest(http.MethodGet, "/test-err", nil)
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	var data map[string]string
	err = json.Unmarshal(body, &data)
	assert.NoError(t, err)
	assert.Equal(t, "invalid request body", data["error"])

	// Test error with details
	req = httptest.NewRequest(http.MethodGet, "/test-details", nil)
	resp, err = app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body, err = io.ReadAll(resp.Body)
	assert.NoError(t, err)
	err = json.Unmarshal(body, &data)
	assert.NoError(t, err)
	assert.Equal(t, "invalid template: bad template syntax", data["error"])
}
