package store_test

import (
	"context"
	"fmt"
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

func TestListPromptsByFilter(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.CreatePrompt(ctx, tm.ID, "prompt_a", "Prompt A", "")
	require.NoError(t, err)
	_, err = store.CreatePrompt(ctx, tm.ID, "prompt_b", "Prompt B", "")
	require.NoError(t, err)

	prompts, err := store.ListPromptsByFilter(ctx, store.PromptFilter{TeamIDs: []uuid.UUID{tm.ID}})
	require.NoError(t, err)
	assert.Len(t, prompts, 2)
}

func TestListPromptsByFilter_Empty(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	prompts, err := store.ListPromptsByFilter(ctx, store.PromptFilter{TeamIDs: []uuid.UUID{tm.ID}})
	require.NoError(t, err)
	assert.Empty(t, prompts)
}

func TestListPrompts_EmptyQueryReturnsEmpty(t *testing.T) {
	// ListPrompts is the FTS function: an empty Q must short-circuit to
	// an empty result set rather than relying on websearch_to_tsquery('')
	// behaviour, which is implementation-defined.
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.CreatePrompt(ctx, tm.ID, "anything", "Anything", "Some description")
	require.NoError(t, err)

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Q:       "",
	})
	require.NoError(t, err)
	assert.Empty(t, prompts, "empty Q must short-circuit to empty results")
}

func TestListPrompts_FTS_WeightedByField(t *testing.T) {
	// Migration 020 assigns weight A to name, B to description, C to slug.
	// A name-only match should outrank a description-only match for the
	// same token, all other things being equal.
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	nameMatch, err := store.CreatePrompt(ctx, tm.ID, "alpha_slug", "Database Tutorial", "unrelated desc")
	require.NoError(t, err)

	descMatch, err := store.CreatePrompt(ctx, tm.ID, "other_slug", "Other Name", "mentions database")
	require.NoError(t, err)

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Q:       "database",
	})
	require.NoError(t, err)
	require.Len(t, prompts, 2, "both prompts should match 'database'")
	assert.Equal(t, nameMatch.ID, prompts[0].ID, "name match (weight A) should rank first")
	assert.Equal(t, descMatch.ID, prompts[1].ID, "description match (weight B) should rank second")
}

func TestListPrompts_FTS_RespectsStatusFilter(t *testing.T) {
	// An archived prompt must not appear in active-only FTS results,
	// even if its tsvector would match the query.
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	active, err := store.CreatePrompt(ctx, tm.ID, "active", "Database Active", "desc")
	require.NoError(t, err)

	archived, err := store.CreatePrompt(ctx, tm.ID, "archived", "Database Archived", "desc")
	require.NoError(t, err)

	require.NoError(t, store.ArchivePrompt(ctx, archived.ID, []uuid.UUID{tm.ID}))

	activeStatus := model.PromptStatusActive
	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Q:       "database",
		Status:  &activeStatus,
	})
	require.NoError(t, err)
	require.Len(t, prompts, 1)
	assert.Equal(t, active.ID, prompts[0].ID)
}

func TestListPrompts_FTS_TeamScope(t *testing.T) {
	// A match in another team must not bleed into the caller's results.
	testutil.SetupDB(t)
	ctx := context.Background()
	tmA, err := store.CreateTeam(ctx, "Team A")
	require.NoError(t, err)
	tmB, err := store.CreateTeam(ctx, "Team B")
	require.NoError(t, err)

	_, err = store.CreatePrompt(ctx, tmA.ID, "a", "Database in A", "desc")
	require.NoError(t, err)
	promptB, err := store.CreatePrompt(ctx, tmB.ID, "b", "Database in B", "desc")
	require.NoError(t, err)

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tmB.ID},
		Q:       "database",
	})
	require.NoError(t, err)
	require.Len(t, prompts, 1)
	assert.Equal(t, promptB.ID, prompts[0].ID)
}

func TestListPrompts_FTS_NoMatch(t *testing.T) {
	// A query with no real lexical overlap should return empty,
	// not every prompt that contains the substring anywhere.
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.CreatePrompt(ctx, tm.ID, "p1", "Refund Helper", "handles refunds")
	require.NoError(t, err)
	_, err = store.CreatePrompt(ctx, tm.ID, "p2", "Shipping Tracker", "tracks parcels")
	require.NoError(t, err)

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Q:       "quantum physics",
	})
	require.NoError(t, err)
	assert.Empty(t, prompts, "out-of-domain query should not match anything")
}

func TestListPrompts_FTS_Limit(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err := store.CreatePrompt(ctx, tm.ID,
			fmt.Sprintf("p%d", i), fmt.Sprintf("Database %d", i), "desc")
		require.NoError(t, err)
	}

	limit := 2
	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Q:       "database",
		Limit:   &limit,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 2)
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

func TestGetPromptBySlug(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	created, err := store.CreatePrompt(ctx, tm.ID, "find_me_slug", "Find Me Slug", "desc")
	require.NoError(t, err)

	got, err := store.GetPromptBySlug(ctx, "find_me_slug", []uuid.UUID{tm.ID})
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, tm.ID, got.TeamID)
	assert.Equal(t, "find_me_slug", got.Slug)
	assert.Equal(t, "Find Me Slug", got.Name)
}

func TestGetPromptBySlug_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	_, err = store.GetPromptBySlug(ctx, "nonexistent", []uuid.UUID{tm.ID})
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

func TestListPromptsByFilter_Filters(t *testing.T) {
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
	prompts, err := store.ListPromptsByFilter(ctx, store.PromptFilter{
		TeamIDs:  []uuid.UUID{tm.ID},
		Archived: &activeOnly,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Test Archived = true (Archived only)
	archivedOnly := true
	prompts, err = store.ListPromptsByFilter(ctx, store.PromptFilter{
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
	prompts, err = store.ListPromptsByFilter(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Tags:    []string{"prod"},
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Filter by non-existent tag
	prompts, err = store.ListPromptsByFilter(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Tags:    []string{"nonexistent_tag"},
	})
	require.NoError(t, err)
	assert.Empty(t, prompts)

	// Filter by Status = "active"
	activeStatus := model.PromptStatusActive
	prompts, err = store.ListPromptsByFilter(ctx, store.PromptFilter{
		TeamIDs: []uuid.UUID{tm.ID},
		Status:  &activeStatus,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Filter by Status = "archived"
	archivedStatus := model.PromptStatusArchived
	prompts, err = store.ListPromptsByFilter(ctx, store.PromptFilter{
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

func TestDeletePrompt(t *testing.T) {
	ctx := context.Background()
	testutil.SetupDB(t)

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreatePrompt(ctx, tm.ID, "my_prompt", "My Prompt", "Initial description")
	require.NoError(t, err)

	// 1. Not found / Unauthorized team delete
	otherTeam, err := store.CreateTeam(ctx, "Other Team")
	require.NoError(t, err)
	err = store.DeletePrompt(ctx, p.ID, []uuid.UUID{otherTeam.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)

	// 2. Success delete
	err = store.DeletePrompt(ctx, p.ID, []uuid.UUID{tm.ID})
	require.NoError(t, err)

	// Verify it's actually deleted
	_, err = store.GetPromptByID(ctx, p.ID, []uuid.UUID{tm.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)

	// 3. Try deleting non-existent prompt
	err = store.DeletePrompt(ctx, nonExistentUUID(), []uuid.UUID{tm.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}
