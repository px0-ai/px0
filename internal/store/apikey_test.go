package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestCreateAPIKey(t *testing.T) {
	testutil.SetupDB(t)

	k, err := store.CreateAPIKey(context.Background(), "my-key", "px0_abc1", "hashvalue")
	require.NoError(t, err)
	assert.NotEmpty(t, k.ID)
	assert.Equal(t, "my-key", k.Name)
	assert.Equal(t, "px0_abc1", k.KeyPrefix)
	assert.Nil(t, k.LastUsedAt)
}

func TestListAPIKeys(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	store.CreateAPIKey(ctx, "key-a", "px0_aaa1", "hash1") //nolint:errcheck
	store.CreateAPIKey(ctx, "key-b", "px0_bbb1", "hash2") //nolint:errcheck

	keys, err := store.ListAPIKeys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 2)
}

func TestListAPIKeys_Empty(t *testing.T) {
	testutil.SetupDB(t)

	keys, err := store.ListAPIKeys(context.Background())
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestGetAPIKeyByHash(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	created, err := store.CreateAPIKey(ctx, "lookup-key", "px0_xyz1", "testhash")
	require.NoError(t, err)

	got, err := store.GetAPIKeyByHash(ctx, "testhash")
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "lookup-key", got.Name)
}

func TestGetAPIKeyByHash_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	_, err := store.GetAPIKeyByHash(context.Background(), "nosuchhash")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteAPIKey(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	k, err := store.CreateAPIKey(ctx, "del-key", "px0_del1", "delhash")
	require.NoError(t, err)

	err = store.DeleteAPIKey(ctx, k.ID)
	require.NoError(t, err)

	_, err = store.GetAPIKeyByHash(ctx, "delhash")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteAPIKey_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	err := store.DeleteAPIKey(context.Background(), nonExistentUUID())
	assert.ErrorIs(t, err, store.ErrNotFound)
}
