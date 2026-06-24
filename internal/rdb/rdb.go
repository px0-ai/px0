package rdb

import (
	"context"
	"fmt"
	"os"

	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

// Connect reads REDIS_URL from the environment and opens a client. Returns an
// error if the variable is absent or the server is unreachable. The caller
// decides whether that error is fatal.
func Connect(ctx context.Context) error {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		return fmt.Errorf("REDIS_URL not set")
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}

	c := redis.NewClient(opts)
	if err := c.Ping(ctx).Err(); err != nil {
		c.Close() //nolint:errcheck
		return fmt.Errorf("ping redis: %w", err)
	}

	Client = c
	return nil
}

func Close() {
	if Client != nil {
		Client.Close() //nolint:errcheck
	}
}
