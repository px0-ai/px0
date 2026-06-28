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

	p, err := store.CreatePrompt(ctx, tm.ID, "greeting", "Greeting", "A greeting prompt")
	require.NoError(t, err)
	assert.NotEmpty(t, p.ID)
	assert.Equal(t, tm.ID, p.TeamID)
	assert.Equal(t, "greeting", p.Slug)
	assert.Equal(t, "Greeting", p.Name)
	assert.Equal(t, "A greeting prompt", p.Description)
	assert.NotZero(t, p.CreatedAt)
}

func TestCreatePrompt_Duplicate(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.CreatePrompt(ctx, tm.ID, "greeting", "Greeting", "A greeting prompt")
	require.NoError(t, err)

	// Duplicate slug/name should return ErrDuplicate
	_, err = store.CreatePrompt(ctx, tm.ID, "greeting", "Other Name", "Another greeting")
	assert.ErrorIs(t, err, store.ErrDuplicate)

	_, err = store.CreatePrompt(ctx, tm.ID, "other_slug", "Greeting", "Another greeting")
	assert.ErrorIs(t, err, store.ErrDuplicate)
}

func TestListPrompts(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.CreatePrompt(ctx, tm.ID, "prompt_a", "Prompt A", "")
	require.NoError(t, err)
	_, err = store.CreatePrompt(ctx, tm.ID, "prompt_b", "Prompt B", "")
	require.NoError(t, err)

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

	created, err := store.CreatePrompt(ctx, tm.ID, "find_me", "Find Me", "desc")
	require.NoError(t, err)

	got, err := store.GetPromptByID(ctx, created.ID, []uuid.UUID{tm.ID})
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, tm.ID, got.TeamID)
	assert.Equal(t, "find_me", got.Slug)
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

	p, err := store.CreatePrompt(ctx, tm.ID, "delete_me", "Delete Me", "")
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
