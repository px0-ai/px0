package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/arpitbhayani/px0/internal/store"
	"github.com/arpitbhayani/px0/internal/testutil"
)

func TestCreateUser(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, "alice@test.com", "hashed")
	require.NoError(t, err)
	assert.NotEmpty(t, user.ID)
	assert.Equal(t, "alice@test.com", user.Email)
	assert.Equal(t, "hashed", user.PasswordHash)
	assert.NotZero(t, user.CreatedAt)
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	_, err := store.CreateUser(ctx, "dup@test.com", "hash1")
	require.NoError(t, err)

	_, err = store.CreateUser(ctx, "dup@test.com", "hash2")
	assert.ErrorIs(t, err, store.ErrDuplicate)
}

func TestGetUserByEmail(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	created, err := store.CreateUser(ctx, "bob@test.com", "hash")
	require.NoError(t, err)

	got, err := store.GetUserByEmail(ctx, "bob@test.com")
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "bob@test.com", got.Email)
	assert.Equal(t, "hash", got.PasswordHash)
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	_, err := store.GetUserByEmail(context.Background(), "ghost@test.com")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestGetUserByID(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	created, err := store.CreateUser(ctx, "carol@test.com", "hash")
	require.NoError(t, err)

	got, err := store.GetUserByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "carol@test.com", got.Email)
}

func TestGetUserByID_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	_, err := store.GetUserByID(context.Background(), nonExistentUUID())
	assert.ErrorIs(t, err, store.ErrNotFound)
}
