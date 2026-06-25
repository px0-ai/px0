package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestCreateSession(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, "session@test.com", "hash")
	require.NoError(t, err)

	expiresAt := time.Now().Add(24 * time.Hour)
	session, err := store.CreateSession(ctx, user.ID, "tok123", expiresAt)
	require.NoError(t, err)
	assert.NotEmpty(t, session.ID)
	assert.Equal(t, user.ID, session.UserID)
	assert.Equal(t, "tok123", session.Token)
	assert.WithinDuration(t, expiresAt, session.ExpiresAt, time.Second)
}

func TestGetSessionByToken(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	user, _ := store.CreateUser(ctx, "getsess@test.com", "hash")
	store.CreateSession(ctx, user.ID, "validtoken", time.Now().Add(time.Hour)) //nolint:errcheck

	got, err := store.GetSessionByToken(ctx, "validtoken")
	require.NoError(t, err)
	assert.Equal(t, user.ID, got.UserID)
	assert.Equal(t, "validtoken", got.Token)
}

func TestGetSessionByToken_Expired(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	user, _ := store.CreateUser(ctx, "expired@test.com", "hash")
	store.CreateSession(ctx, user.ID, "expiredtok", time.Now().Add(-time.Hour)) //nolint:errcheck

	_, err := store.GetSessionByToken(ctx, "expiredtok")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestGetSessionByToken_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	_, err := store.GetSessionByToken(context.Background(), "nosuchtoken")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteSession(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	user, _ := store.CreateUser(ctx, "del@test.com", "hash")
	store.CreateSession(ctx, user.ID, "deltok", time.Now().Add(time.Hour)) //nolint:errcheck

	err := store.DeleteSession(ctx, "deltok")
	require.NoError(t, err)

	_, err = store.GetSessionByToken(ctx, "deltok")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

// Cache-specific tests. These call testutil.SetupRedis and are skipped
// automatically when Redis is not available.

func TestCreateSession_PopulatesCache(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SetupRedis(t)
	ctx := context.Background()

	user, _ := store.CreateUser(ctx, "cachepop@test.com", "hash")
	store.CreateSession(ctx, user.ID, "cachedtok", time.Now().Add(time.Hour)) //nolint:errcheck

	// Delete the row from Postgres so the only copy lives in Redis.
	db.Pool.Exec(ctx, "DELETE FROM sessions WHERE token = $1", "cachedtok") //nolint:errcheck

	// Should still be found via the cache.
	got, err := store.GetSessionByToken(ctx, "cachedtok")
	require.NoError(t, err)
	assert.Equal(t, "cachedtok", got.Token)
}

func TestGetSessionByToken_BackfillsCache(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SetupRedis(t)
	ctx := context.Background()

	user, _ := store.CreateUser(ctx, "backfill@test.com", "hash")
	// Create directly in Postgres (rdb.Client is set but not used by the insert yet).
	store.CreateSession(ctx, user.ID, "backfilltok", time.Now().Add(time.Hour)) //nolint:errcheck

	// First lookup goes to Postgres and back-fills the cache.
	_, err := store.GetSessionByToken(ctx, "backfilltok")
	require.NoError(t, err)

	// Remove from Postgres; subsequent lookup must come from cache.
	db.Pool.Exec(ctx, "DELETE FROM sessions WHERE token = $1", "backfilltok") //nolint:errcheck

	got, err := store.GetSessionByToken(ctx, "backfilltok")
	require.NoError(t, err)
	assert.Equal(t, "backfilltok", got.Token)
}

func TestDeleteSession_EvictsCache(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SetupRedis(t)
	ctx := context.Background()

	user, _ := store.CreateUser(ctx, "evict@test.com", "hash")
	store.CreateSession(ctx, user.ID, "evicttok", time.Now().Add(time.Hour)) //nolint:errcheck

	err := store.DeleteSession(ctx, "evicttok")
	require.NoError(t, err)

	// Must not be findable via cache either.
	_, err = store.GetSessionByToken(ctx, "evicttok")
	assert.ErrorIs(t, err, store.ErrNotFound)
}
