package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func maskDSN(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parts := strings.SplitN(dsn, "@", 2)
		if len(parts) == 2 {
			prefix := parts[0]
			subParts := strings.SplitN(prefix, "://", 2)
			if len(subParts) == 2 {
				userPass := subParts[1]
				userPassParts := strings.SplitN(userPass, ":", 2)
				if len(userPassParts) == 2 {
					return fmt.Sprintf("%s://%s:***@%s", subParts[0], userPassParts[0], parts[1])
				}
			}
		}
	}
	return dsn
}

func maskRedisURL(url string) string {
	if strings.HasPrefix(url, "redis://") {
		parts := strings.SplitN(url, "@", 2)
		if len(parts) == 2 {
			prefix := parts[0]
			subParts := strings.SplitN(prefix, "://", 2)
			if len(subParts) == 2 {
				userPass := subParts[1]
				if strings.Contains(userPass, ":") {
					userPassParts := strings.SplitN(userPass, ":", 2)
					return fmt.Sprintf("redis://%s:***@%s", userPassParts[0], parts[1])
				} else if userPass != "" {
					return fmt.Sprintf("redis://***@%s", parts[1])
				}
			}
		}
	}
	return url
}

func main() {
	force := flag.Bool("force", false, "Skip confirmation prompt")
	drop := flag.Bool("drop", false, "Drop all tables completely instead of truncating data")
	flag.Parse()

	_ = godotenv.Load()

	postgresURLs := []string{}
	seenPostgres := map[string]bool{}
	for _, envVar := range []string{"DATABASE_URL", "TEST_DATABASE_URL"} {
		url := os.Getenv(envVar)
		if url == "" {
			if envVar == "DATABASE_URL" {
				url = "postgres://px0:px0secret@localhost:5432/px0?sslmode=disable"
			} else {
				url = "postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable"
			}
		}
		if !seenPostgres[url] {
			seenPostgres[url] = true
			postgresURLs = append(postgresURLs, url)
		}
	}

	redisURLs := []string{}
	seenRedis := map[string]bool{}
	for _, envVar := range []string{"REDIS_URL", "TEST_REDIS_URL"} {
		url := os.Getenv(envVar)
		if url == "" {
			if envVar == "REDIS_URL" {
				url = "redis://localhost:6379"
			} else {
				url = "redis://localhost:6379/1"
			}
		}
		if !seenRedis[url] {
			seenRedis[url] = true
			redisURLs = append(redisURLs, url)
		}
	}

	fmt.Println("CRITICAL ACTION REQUIRED: Destroy Database Data")
	fmt.Println("-----------------------------------------------")
	fmt.Println("The following databases are targeted for cleanup:")
	fmt.Println("PostgreSQL:")
	for _, url := range postgresURLs {
		fmt.Printf("  - %s\n", maskDSN(url))
	}
	fmt.Println("Redis:")
	for _, url := range redisURLs {
		fmt.Printf("  - %s\n", maskRedisURL(url))
	}
	fmt.Println()

	if !*force {
		fmt.Print("Are you absolutely sure you want to clean up all data in these databases? (y/N): ")
		var response string
		_, err := fmt.Scanln(&response)
		if err != nil || (strings.ToLower(response) != "y" && strings.ToLower(response) != "yes") {
			fmt.Println("Operation aborted.")
			os.Exit(0)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Println("\nStarting cleanup...")

	for _, dsn := range postgresURLs {
		err := destroyPostgres(ctx, dsn, *drop)
		if err != nil {
			fmt.Printf("[PostgreSQL ERROR] Failed to clean %s: %v\n", maskDSN(dsn), err)
		}
	}

	for _, rURL := range redisURLs {
		err := destroyRedis(ctx, rURL)
		if err != nil {
			fmt.Printf("[Redis ERROR] Failed to clean %s: %v\n", maskRedisURL(rURL), err)
		}
	}

	fmt.Println("\nCleanup completed.")
}

func destroyPostgres(ctx context.Context, dsn string, drop bool) error {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}

	// Adjust pool settings for quick connection/destruction
	config.MaxConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	if drop {
		rows, err := pool.Query(ctx, `
			SELECT table_name 
			FROM information_schema.tables 
			WHERE table_schema = 'public' 
			  AND table_type = 'BASE TABLE'
		`)
		if err != nil {
			return fmt.Errorf("query tables: %w", err)
		}
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				return fmt.Errorf("scan table name: %w", err)
			}
			tables = append(tables, table)
		}

		if len(tables) == 0 {
			fmt.Printf("[PostgreSQL] No tables to drop in %s\n", maskDSN(dsn))
			return nil
		}

		quotedTables := make([]string, len(tables))
		for i, t := range tables {
			quotedTables[i] = fmt.Sprintf("\"%s\"", t)
		}

		query := fmt.Sprintf("DROP TABLE %s CASCADE", strings.Join(quotedTables, ", "))
		_, err = pool.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("execute drop: %w", err)
		}

		fmt.Printf("[PostgreSQL] Successfully dropped %d tables in %s\n", len(tables), maskDSN(dsn))
	} else {
		rows, err := pool.Query(ctx, `
			SELECT table_name 
			FROM information_schema.tables 
			WHERE table_schema = 'public' 
			  AND table_type = 'BASE TABLE' 
			  AND table_name != 'schema_migrations'
		`)
		if err != nil {
			return fmt.Errorf("query tables: %w", err)
		}
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				return fmt.Errorf("scan table name: %w", err)
			}
			tables = append(tables, table)
		}

		if len(tables) == 0 {
			fmt.Printf("[PostgreSQL] No tables to truncate in %s\n", maskDSN(dsn))
			return nil
		}

		quotedTables := make([]string, len(tables))
		for i, t := range tables {
			quotedTables[i] = fmt.Sprintf("\"%s\"", t)
		}

		query := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", strings.Join(quotedTables, ", "))
		_, err = pool.Exec(ctx, query)
		if err != nil {
			return fmt.Errorf("execute truncate: %w", err)
		}

		fmt.Printf("[PostgreSQL] Successfully truncated %d tables in %s\n", len(tables), maskDSN(dsn))
	}

	return nil
}

func destroyRedis(ctx context.Context, url string) error {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)
	defer client.Close()

	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}

	if err := client.FlushAll(ctx).Err(); err != nil {
		return fmt.Errorf("flush redis: %w", err)
	}

	fmt.Printf("[Redis] Successfully flushed all keys in Redis at %s\n", maskRedisURL(url))
	return nil
}
