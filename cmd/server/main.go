package main

import (
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"

	"github.com/arpitbhayani/px0/internal/handler"
)

func main() {
	_ = godotenv.Load()

	app := fiber.New(fiber.Config{
		AppName: "px0",
	})

	app.Get("/v1/health", handler.Hello)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Fatal(app.Listen(":" + port))
}
