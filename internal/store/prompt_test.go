package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/model"
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

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{TeamIDs: []uuid.UUID{tm.ID}})
	require.NoError(t, err)
	assert.Len(t, prompts, 2)
}

func TestListPrompts_Empty(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{TeamIDs: []uuid.UUID{tm.ID}})
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

func TestArchivePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreatePrompt(ctx, tm.ID, "archive_me", "Archive Me", "")
	require.NoError(t, err)
	assert.Equal(t, model.PromptStatusActive, p.Status)

	err = store.ArchivePrompt(ctx, p.ID, []uuid.UUID{tm.ID})
	require.NoError(t, err)

	got, err := store.GetPromptByID(ctx, p.ID, []uuid.UUID{tm.ID})
	require.NoError(t, err)
	assert.Equal(t, model.PromptStatusArchived, got.Status)
}

func TestArchivePrompt_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	err = store.ArchivePrompt(ctx, nonExistentUUID(), []uuid.UUID{tm.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestListPrompts_Filters(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	pA, err := store.CreatePrompt(ctx, tm.ID, "prompt_a", "Prompt A", "")
	require.NoError(t, err)
	pB, err := store.CreatePrompt(ctx, tm.ID, "prompt_b", "Prompt B", "")
	require.NoError(t, err)

	// 1. Archive pB
	err = store.ArchivePrompt(ctx, pB.ID, []uuid.UUID{tm.ID})
	require.NoError(t, err)

	// Test Archived = false (Active only)
	activeOnly := false
	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs:  []uuid.UUID{tm.ID},
		Archived: &activeOnly,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Test Archived = true (Archived only)
	archivedOnly := true
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs:  []uuid.UUID{tm.ID},
		Archived: &archivedOnly,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pB.ID, prompts[0].ID)

	// 2. Create version and tag for pA
	v, err := store.CreateVersion(ctx, pA.ID, "template")
	require.NoError(t, err)
	err = store.SetTag(ctx, pA.ID, v.Version, "prod")
	require.NoError(t, err)

	// Filter by tag "prod"
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Tags:    []string{"prod"},
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Filter by non-existent tag
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Tags:    []string{"nonexistent_tag"},
	})
	require.NoError(t, err)
	assert.Empty(t, prompts)

	// Filter by Status = "active"
	activeStatus := model.PromptStatusActive
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Status:  &activeStatus,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Filter by Status = "archived"
	archivedStatus := model.PromptStatusArchived
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Status:  &archivedStatus,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pB.ID, prompts[0].ID)
}

func TestUpdatePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreatePrompt(ctx, tm.ID, "my_prompt", "My Prompt", "Initial description")
	require.NoError(t, err)

	// 1. Success update
	updated, err := store.UpdatePrompt(ctx, p.ID, []uuid.UUID{tm.ID}, "Updated description")
	require.NoError(t, err)
	assert.Equal(t, p.ID, updated.ID)
	assert.Equal(t, "my_prompt", updated.Slug) // slug remains unchanged
	assert.Equal(t, "My Prompt", updated.Name)  // name remains unchanged
	assert.Equal(t, "Updated description", updated.Description)

	// 2. Not found / Unauthorized team update
	otherTeam, err := store.CreateTeam(ctx, "Other Team")
	require.NoError(t, err)
	_, err = store.UpdatePrompt(ctx, p.ID, []uuid.UUID{otherTeam.ID}, "Updated description")
	assert.ErrorIs(t, err, store.ErrNotFound)

	_, err = store.UpdatePrompt(ctx, nonExistentUUID(), []uuid.UUID{tm.ID}, "Updated description")
	assert.ErrorIs(t, err, store.ErrNotFound)
}
