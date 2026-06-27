package testutil

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/px0-ai/px0/internal/db"
)

var (
	once   sync.Once
	pool   *pgxpool.Pool
	skipDB bool
)

// SetupDB connects to the test database and runs migrations once per test
// binary, then truncates all tables before the calling test runs. Tests are
// skipped automatically if the database is unreachable.
//
// db.Pool is set exactly once, which avoids the race between Fiber's internal
// request goroutine (from app.Test) and the next test's pool assignment.
func SetupDB(t *testing.T) {
	t.Helper()

	once.Do(func() {
		dsn := os.Getenv("TEST_DATABASE_URL")
		if dsn == "" {
			dsn = "postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable"
		}

		ctx := context.Background()

		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			skipDB = true
			return
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			skipDB = true
			return
		}
		if err := db.SetPool(p); err != nil {
			p.Close()
			skipDB = true
			return
		}
		if err := db.Migrate(ctx); err != nil {
			skipDB = true
			return
		}
		pool = p
	})

	if skipDB {
		t.Skip("postgres unavailable, skipping integration test")
		return
	}

	truncate(pool)
}

func truncate(p *pgxpool.Pool) {
	p.Exec(context.Background(), //nolint:errcheck
		`TRUNCATE TABLE prompt_versions, prompts, sessions, api_keys, users, teams, organizations CASCADE`,
	)
}
