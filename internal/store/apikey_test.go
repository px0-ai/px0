package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestCreateAPIKey(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	org, err := store.CreateOrganization(ctx, "Test Org")
	require.NoError(t, err)

	tm, err := store.CreateTeamWithOrg(ctx, "Test Team", org.ID)
	require.NoError(t, err)

	k, err := store.CreateAPIKey(ctx, "my-key", org.ID, []uuid.UUID{tm.ID}, "read_render", "hashvalue")
	require.NoError(t, err)
	assert.NotEmpty(t, k.ID)
	assert.Equal(t, "my-key", k.Name)
	assert.Equal(t, org.ID, k.OrgID)
	assert.Equal(t, tm.ID, *k.TeamID)
	assert.Equal(t, "read_render", k.Operation)
	assert.Nil(t, k.LastUsedAt)
}

func TestListAPIKeys(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	org, err := store.CreateOrganization(ctx, "Test Org")
	require.NoError(t, err)

	tm, err := store.CreateTeamWithOrg(ctx, "Test Team", org.ID)
	require.NoError(t, err)

	_, err = store.CreateAPIKey(ctx, "key-a", org.ID, []uuid.UUID{tm.ID}, "read_render", "hash1")
	require.NoError(t, err)
	_, err = store.CreateAPIKey(ctx, "key-b", org.ID, []uuid.UUID{tm.ID}, "all", "hash2")
	require.NoError(t, err)

	keys, err := store.ListAPIKeysForOrg(ctx, org.ID)
	require.NoError(t, err)
	assert.Len(t, keys, 2)
}

func TestListAPIKeys_Empty(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	org, err := store.CreateOrganization(ctx, "Test Org")
	require.NoError(t, err)

	keys, err := store.ListAPIKeysForOrg(ctx, org.ID)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestGetAPIKeyByHash(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	org, err := store.CreateOrganization(ctx, "Test Org")
	require.NoError(t, err)

	tm, err := store.CreateTeamWithOrg(ctx, "Test Team", org.ID)
	require.NoError(t, err)

	created, err := store.CreateAPIKey(ctx, "lookup-key", org.ID, []uuid.UUID{tm.ID}, "read_render", "testhash")
	require.NoError(t, err)

	got, err := store.GetAPIKeyByHash(ctx, "testhash")
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "lookup-key", got.Name)
	assert.Equal(t, org.ID, got.OrgID)
	assert.Equal(t, tm.ID, *got.TeamID)
}

func TestGetAPIKeyByHash_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	_, err := store.GetAPIKeyByHash(context.Background(), "nosuchhash")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteAPIKey(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	org, err := store.CreateOrganization(ctx, "Test Org")
	require.NoError(t, err)

	tm, err := store.CreateTeamWithOrg(ctx, "Test Team", org.ID)
	require.NoError(t, err)

	k, err := store.CreateAPIKey(ctx, "del-key", org.ID, []uuid.UUID{tm.ID}, "read_render", "delhash")
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
