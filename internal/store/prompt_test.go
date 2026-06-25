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

func TestCreatePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreatePrompt(ctx, "Greeting", "A greeting prompt", []uuid.UUID{tm.ID})
	require.NoError(t, err)
	assert.NotEmpty(t, p.ID)
	assert.Equal(t, "Greeting", p.Name)
	assert.Equal(t, "A greeting prompt", p.Description)
	assert.NotZero(t, p.CreatedAt)
}

func TestListPrompts(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	store.CreatePrompt(ctx, "Prompt A", "", []uuid.UUID{tm.ID}) //nolint:errcheck
	store.CreatePrompt(ctx, "Prompt B", "", []uuid.UUID{tm.ID}) //nolint:errcheck

	prompts, err := store.ListPrompts(ctx, []uuid.UUID{tm.ID})
	require.NoError(t, err)
	assert.Len(t, prompts, 2)
}

func TestListPrompts_Empty(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	prompts, err := store.ListPrompts(ctx, []uuid.UUID{tm.ID})
	require.NoError(t, err)
	assert.Empty(t, prompts)
}

func TestGetPromptByID(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	created, err := store.CreatePrompt(ctx, "Find Me", "desc", []uuid.UUID{tm.ID})
	require.NoError(t, err)

	got, err := store.GetPromptByID(ctx, created.ID, []uuid.UUID{tm.ID})
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "Find Me", got.Name)
}

func TestGetPromptByID_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.GetPromptByID(ctx, nonExistentUUID(), []uuid.UUID{tm.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeletePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreatePrompt(ctx, "Delete Me", "", []uuid.UUID{tm.ID})
	require.NoError(t, err)

	err = store.DeletePrompt(ctx, p.ID, []uuid.UUID{tm.ID})
	require.NoError(t, err)

	_, err = store.GetPromptByID(ctx, p.ID, []uuid.UUID{tm.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeletePrompt_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	err = store.DeletePrompt(ctx, nonExistentUUID(), []uuid.UUID{tm.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}
