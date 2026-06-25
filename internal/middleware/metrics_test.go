package middleware_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/arpitbhayani/px0/internal/middleware"
)

func TestMetricsMiddleware_Success(t *testing.T) {
	app := fiber.New()
	app.Use(middleware.Metrics())

	app.Get("/test-success", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest("GET", "/test-success", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("expected status %d, got %d", fiber.StatusOK, resp.StatusCode)
	}
}

func TestMetricsMiddleware_Error(t *testing.T) {
	app := fiber.New()
	app.Use(middleware.Metrics())

	app.Get("/test-error", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusBadRequest, "bad request error")
	})

	app.Get("/test-panic-err", func(c *fiber.Ctx) error {
		return errors.New("custom generic error")
	})

	// Test standard Fiber error
	req1 := httptest.NewRequest("GET", "/test-error", nil)
	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != fiber.StatusBadRequest {
		t.Errorf("expected status %d, got %d", fiber.StatusBadRequest, resp1.StatusCode)
	}

	// Test generic error (which results in 500)
	req2 := httptest.NewRequest("GET", "/test-panic-err", nil)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", fiber.StatusInternalServerError, resp2.StatusCode)
	}
}
