package main

import (
	"context"
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/arpitbhayani/px0/internal/app"
	"github.com/arpitbhayani/px0/internal/db"
	"github.com/arpitbhayani/px0/internal/rdb"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

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
