package handler_test

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/arpitbhayani/px0/internal/handler"
)

func TestHello(t *testing.T) {
	app := fiber.New()
	app.Get("/v1/health", handler.Hello)

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
	if string(body) == "" {
		t.Fatal("expected non-empty body")
	}
}
