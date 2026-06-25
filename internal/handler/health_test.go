package handler_test

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/px0-ai/px0/internal/handler"
)

func TestHealth(t *testing.T) {
	app := fiber.New()
	app.Get("/v1/health", handler.Health)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	expected := `{"status":"OK"}`
	if string(body) != expected {
		t.Fatalf("expected %s, got %s", expected, string(body))
	}
}
