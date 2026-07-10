package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/px0-ai/px0/internal/app"
	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/embedderfactory"
	"github.com/px0-ai/px0/internal/rdb"
	"github.com/px0-ai/px0/internal/search"
	"github.com/px0-ai/px0/internal/searchfactory"
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

	// 3. Initialize embedder provider
	embedder, err := embedderfactory.NewEmbedder()
	if err != nil {
		log.Printf("warn: embedder provider unavailable: %v", err)
	} else if embedder != nil {
		search.SetEmbedder(embedder)
	}

	// 4. Initialize search providers (FTS and vector, independent).
	ftsProvider, err := searchfactory.NewFTSProvider(ctx)
	if err != nil {
		log.Printf("warn: FTS search provider unavailable, FTS search is disabled: %v", err)
		ftsProvider = search.NoopProvider{}
	}
	vectorProvider, err := searchfactory.NewVectorProvider(ctx)
	if err != nil {
		log.Printf("warn: vector search provider unavailable, vector search is disabled: %v", err)
		vectorProvider = search.NoopProvider{}
	}
	search.Init(ftsProvider, vectorProvider)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Fatal(app.New().Listen(":" + port))
}
