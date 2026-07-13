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

// newProject creates a team and a project owned by it, returning the project.
func newProject(t *testing.T, ctx context.Context, name string) *model.Project {
	t.Helper()
	tm, err := store.CreateTeam(ctx, name+" Team")
	require.NoError(t, err)
	proj, err := store.CreateProject(ctx, tm.ID, "default", name+" Project")
	require.NoError(t, err)
	return proj
}

func TestCreatePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	p, err := store.CreatePrompt(ctx, proj.ID, "greeting", "Greeting", "A greeting prompt")
	require.NoError(t, err)
	assert.NotEmpty(t, p.ID)
	assert.Equal(t, proj.ID, p.ProjectID)
	assert.Equal(t, "greeting", p.Slug)
	assert.Equal(t, "Greeting", p.Name)
	assert.Equal(t, "A greeting prompt", p.Description)
	assert.NotZero(t, p.CreatedAt)
}

func TestCreatePrompt_Duplicate(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	_, err := store.CreatePrompt(ctx, proj.ID, "greeting", "Greeting", "A greeting prompt")
	require.NoError(t, err)

	// Duplicate slug/name within the same project should return ErrDuplicate.
	_, err = store.CreatePrompt(ctx, proj.ID, "greeting", "Other Name", "Another greeting")
	assert.ErrorIs(t, err, store.ErrDuplicate)

	_, err = store.CreatePrompt(ctx, proj.ID, "other_slug", "Greeting", "Another greeting")
	assert.ErrorIs(t, err, store.ErrDuplicate)

	// The same name/slug under a different project is allowed.
	other := newProject(t, ctx, "Other")
	_, err = store.CreatePrompt(ctx, other.ID, "greeting", "Greeting", "A greeting prompt")
	require.NoError(t, err)
}

func TestListPrompts(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	_, err := store.CreatePrompt(ctx, proj.ID, "prompt_a", "Prompt A", "")
	require.NoError(t, err)
	_, err = store.CreatePrompt(ctx, proj.ID, "prompt_b", "Prompt B", "")
	require.NoError(t, err)

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{ProjectIDs: []uuid.UUID{proj.ID}})
	require.NoError(t, err)
	assert.Len(t, prompts, 2)
}

func TestListPrompts_Empty(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	prompts, err := store.ListPrompts(ctx, store.PromptFilter{ProjectIDs: []uuid.UUID{proj.ID}})
	require.NoError(t, err)
	assert.Empty(t, prompts)
}

func TestGetPromptByID(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	created, err := store.CreatePrompt(ctx, proj.ID, "find_me", "Find Me", "desc")
	require.NoError(t, err)

	got, err := store.GetPromptByID(ctx, created.ID, []uuid.UUID{proj.ID})
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, proj.ID, got.ProjectID)
	assert.Equal(t, "find_me", got.Slug)
	assert.Equal(t, "Find Me", got.Name)
}

func TestGetPromptByID_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	_, err := store.GetPromptByID(ctx, nonExistentUUID(), []uuid.UUID{proj.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestGetPromptBySlug(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	created, err := store.CreatePrompt(ctx, proj.ID, "find_me_slug", "Find Me Slug", "desc")
	require.NoError(t, err)

	got, err := store.GetPromptBySlug(ctx, "find_me_slug", []uuid.UUID{proj.ID})
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, proj.ID, got.ProjectID)
	assert.Equal(t, "find_me_slug", got.Slug)
	assert.Equal(t, "Find Me Slug", got.Name)
}

func TestGetPromptBySlug_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	_, err := store.GetPromptBySlug(ctx, "nonexistent", []uuid.UUID{proj.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestArchivePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	p, err := store.CreatePrompt(ctx, proj.ID, "archive_me", "Archive Me", "")
	require.NoError(t, err)
	assert.Equal(t, model.PromptStatusActive, p.Status)

	err = store.ArchivePrompt(ctx, p.ID, []uuid.UUID{proj.ID})
	require.NoError(t, err)

	got, err := store.GetPromptByID(ctx, p.ID, []uuid.UUID{proj.ID})
	require.NoError(t, err)
	assert.Equal(t, model.PromptStatusArchived, got.Status)
}

func TestArchivePrompt_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	err := store.ArchivePrompt(ctx, nonExistentUUID(), []uuid.UUID{proj.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestListPrompts_Filters(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	pA, err := store.CreatePrompt(ctx, proj.ID, "prompt_a", "Prompt A", "")
	require.NoError(t, err)
	pB, err := store.CreatePrompt(ctx, proj.ID, "prompt_b", "Prompt B", "")
	require.NoError(t, err)

	// 1. Archive pB
	err = store.ArchivePrompt(ctx, pB.ID, []uuid.UUID{proj.ID})
	require.NoError(t, err)

	// Test Archived = false (Active only)
	activeOnly := false
	prompts, err := store.ListPrompts(ctx, store.PromptFilter{
		ProjectIDs: []uuid.UUID{proj.ID},
		Archived:   &activeOnly,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Test Archived = true (Archived only)
	archivedOnly := true
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		ProjectIDs: []uuid.UUID{proj.ID},
		Archived:   &archivedOnly,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pB.ID, prompts[0].ID)

	// 2. Create version and tag for pA
	v, err := store.CreateVersion(ctx, pA.ID, store.CreateVersionParams{Template: "template"})
	require.NoError(t, err)
	err = store.SetTag(ctx, pA.ID, v.Version, "prod")
	require.NoError(t, err)

	// Filter by tag "prod"
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		ProjectIDs: []uuid.UUID{proj.ID},
		Tags:       []string{"prod"},
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Filter by non-existent tag
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		ProjectIDs: []uuid.UUID{proj.ID},
		Tags:       []string{"nonexistent_tag"},
	})
	require.NoError(t, err)
	assert.Empty(t, prompts)

	// Filter by Status = "active"
	activeStatus := model.PromptStatusActive
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		ProjectIDs: []uuid.UUID{proj.ID},
		Status:     &activeStatus,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pA.ID, prompts[0].ID)

	// Filter by Status = "archived"
	archivedStatus := model.PromptStatusArchived
	prompts, err = store.ListPrompts(ctx, store.PromptFilter{
		ProjectIDs: []uuid.UUID{proj.ID},
		Status:     &archivedStatus,
	})
	require.NoError(t, err)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pB.ID, prompts[0].ID)
}

func TestUpdatePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Test")

	p, err := store.CreatePrompt(ctx, proj.ID, "my_prompt", "My Prompt", "Initial description")
	require.NoError(t, err)

	// 1. Success update
	updated, err := store.UpdatePrompt(ctx, p.ID, []uuid.UUID{proj.ID}, "Updated description")
	require.NoError(t, err)
	assert.Equal(t, p.ID, updated.ID)
	assert.Equal(t, "my_prompt", updated.Slug) // slug remains unchanged
	assert.Equal(t, "My Prompt", updated.Name) // name remains unchanged
	assert.Equal(t, "Updated description", updated.Description)

	// 2. Not found / Unauthorized project update
	otherProject := newProject(t, ctx, "Other")
	_, err = store.UpdatePrompt(ctx, p.ID, []uuid.UUID{otherProject.ID}, "Updated description")
	assert.ErrorIs(t, err, store.ErrNotFound)

	_, err = store.UpdatePrompt(ctx, nonExistentUUID(), []uuid.UUID{proj.ID}, "Updated description")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestMovePrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	source := newProject(t, ctx, "Source")
	target := newProject(t, ctx, "Target")

	p, err := store.CreatePrompt(ctx, source.ID, "movable", "Movable", "")
	require.NoError(t, err)

	// Success: move into the target project.
	err = store.MovePrompt(ctx, p.ID, []uuid.UUID{source.ID}, target.ID)
	require.NoError(t, err)

	moved, err := store.GetPromptByID(ctx, p.ID, []uuid.UUID{target.ID})
	require.NoError(t, err)
	assert.Equal(t, target.ID, moved.ProjectID)

	// It no longer belongs to the source project.
	_, err = store.GetPromptByID(ctx, p.ID, []uuid.UUID{source.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestMovePrompt_TargetCollision(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	source := newProject(t, ctx, "Source")
	target := newProject(t, ctx, "Target")

	p, err := store.CreatePrompt(ctx, source.ID, "greeting", "Greeting", "")
	require.NoError(t, err)
	// Target already has a prompt with the same name/slug.
	_, err = store.CreatePrompt(ctx, target.ID, "greeting", "Greeting", "")
	require.NoError(t, err)

	err = store.MovePrompt(ctx, p.ID, []uuid.UUID{source.ID}, target.ID)
	assert.ErrorIs(t, err, store.ErrDuplicate)
}

func TestDeleteProject_CascadesPrompts(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	proj := newProject(t, ctx, "Doomed")

	p, err := store.CreatePrompt(ctx, proj.ID, "doomed_prompt", "Doomed Prompt", "")
	require.NoError(t, err)

	require.NoError(t, store.DeleteProject(ctx, proj.ID))

	_, err = store.GetPromptByID(ctx, p.ID, []uuid.UUID{proj.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}
