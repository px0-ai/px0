package testutil

import (
	"context"
	"os"
	"testing"

	goredis "github.com/redis/go-redis/v9"

	"github.com/arpitbhayani/px0/internal/rdb"
)

// SetupRedis connects to the test Redis instance and flushes the test database
// after the test completes. Tests are skipped if Redis is unreachable.
// Use Redis DB 1 for tests to isolate from the default dev DB 0.
func SetupRedis(t *testing.T) {
	t.Helper()

	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379/1"
	}

	opts, err := goredis.ParseURL(url)
	if err != nil {
		t.Skipf("invalid TEST_REDIS_URL, skipping cache test: %v", err)
		return
	}

	client := goredis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close() //nolint:errcheck
		t.Skipf("redis unavailable, skipping cache test: %v", err)
		return
	}

	rdb.Client = client

	t.Cleanup(func() {
		client.FlushDB(context.Background()) //nolint:errcheck
		client.Close()                       //nolint:errcheck
		rdb.Client = nil
	})
}
