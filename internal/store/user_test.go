package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
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

func TestPasswordResetsStore(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	// 1. Create a user
	user, err := store.CreateUser(ctx, "reset@test.com", "old_hash")
	require.NoError(t, err)

	// 2. Create a password reset request
	code := "123456"
	expiresAt := time.Now().Add(15 * time.Minute)
	err = store.CreatePasswordReset(ctx, user.ID, code, expiresAt)
	require.NoError(t, err)

	// 3. Lookup the password reset request
	derivedUserID, exp, err := store.GetPasswordResetByCode(ctx, code)
	require.NoError(t, err)
	assert.Equal(t, user.ID, derivedUserID)
	assert.WithinDuration(t, expiresAt, exp, time.Second)

	// 4. Update user's password
	err = store.UpdateUserPassword(ctx, user.ID, "new_hash")
	require.NoError(t, err)

	// Verify password hash was updated
	updatedUser, err := store.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "new_hash", updatedUser.PasswordHash)

	// 5. Delete the password reset request
	err = store.DeletePasswordReset(ctx, code)
	require.NoError(t, err)

	// Verify it is no longer retrievable
	_, _, err = store.GetPasswordResetByCode(ctx, code)
	assert.ErrorIs(t, err, store.ErrNotFound)
}
