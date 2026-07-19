package main

import (
	"context"
	"log"

	"github.com/joho/godotenv"

	"github.com/px0-ai/px0/internal/db"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	if err := db.Connect(ctx); err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer db.Close()

	log.Println("info: running database migrations...")
	if err := db.Migrate(ctx); err != nil {
		log.Fatalf("run migrations failed: %v", err)
	}

	log.Println("info: database migrations applied successfully")
}
