package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/px0-ai/px0/internal/app"
	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/rdb"
	"github.com/px0-ai/px0/internal/telemetry"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	// Initialize OpenTelemetry metrics SDK
	otelShutdown, err := telemetry.InitMetrics(ctx)
	if err != nil {
		log.Printf("warn: failed to initialize OpenTelemetry metrics: %v", err)
	} else {
		defer otelShutdown()
		log.Println("info: OpenTelemetry metrics initialized successfully")
	}

	if err := db.Connect(ctx); err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	if err := rdb.Connect(ctx); err != nil {
		log.Printf("warn: redis unavailable, sessions will not be cached: %v", err)
	}
	defer rdb.Close()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Fatal(app.New().Listen(":" + port))
}
