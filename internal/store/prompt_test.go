package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestCreatePrompt(t *testing.T) {
	testutil.SetupDB(t)

	p, err := store.CreatePrompt(context.Background(), "Greeting", "A greeting prompt")
	require.NoError(t, err)
	assert.NotEmpty(t, p.ID)
	assert.Equal(t, "Greeting", p.Name)
	assert.Equal(t, "A greeting prompt", p.Description)
	assert.NotZero(t, p.CreatedAt)
}

func TestListPrompts(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	store.CreatePrompt(ctx, "Prompt A", "") //nolint:errcheck
	store.CreatePrompt(ctx, "Prompt B", "") //nolint:errcheck

	prompts, err := store.ListPrompts(ctx)
	require.NoError(t, err)
	assert.Len(t, prompts, 2)
}

func TestListPrompts_Empty(t *testing.T) {
	testutil.SetupDB(t)

	prompts, err := store.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, prompts)
}

func TestGetPromptByID(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	created, err := store.CreatePrompt(ctx, "Find Me", "desc")
	require.NoError(t, err)

	got, err := store.GetPromptByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "Find Me", got.Name)
}

func TestGetPromptByID_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	_, err := store.GetPromptByID(context.Background(), nonExistentUUID())
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeletePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	p, err := store.CreatePrompt(ctx, "Delete Me", "")
	require.NoError(t, err)

	err = store.DeletePrompt(ctx, p.ID)
	require.NoError(t, err)

	_, err = store.GetPromptByID(ctx, p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeletePrompt_NotFound(t *testing.T) {
	testutil.SetupDB(t)

	err := store.DeletePrompt(context.Background(), nonExistentUUID())
	assert.ErrorIs(t, err, store.ErrNotFound)
}
