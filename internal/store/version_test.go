package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func newPrompt(t *testing.T, ctx context.Context) *model.Prompt {
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)
	p, err := store.CreatePrompt(ctx, tm.ID, "my-prompt", "My Prompt", "Desc")
	require.NoError(t, err)
	return p
}

func TestCreateVersion(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v, err := store.CreateVersion(ctx, p.ID, "template content")
	require.NoError(t, err)
	assert.Equal(t, p.ID, v.PromptID)
	assert.Equal(t, 1, v.Version)
	assert.Equal(t, "template content", v.Template)
	assert.Equal(t, model.VersionStatusDraft, v.Status)
	assert.NotNil(t, v.CreatedAt)
	assert.Nil(t, v.PublishedAt)

	v2, err := store.CreateVersion(ctx, p.ID, "v2 template")
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Version)
}

func TestListVersions(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v1, err := store.CreateVersion(ctx, p.ID, "v1")
	require.NoError(t, err)
	v2, err := store.CreateVersion(ctx, p.ID, "v2")
	require.NoError(t, err)

	_, err = store.PromoteVersion(ctx, p.ID, v1.Version) // draft -> stable
	require.NoError(t, err)
	_, err = store.PromoteVersion(ctx, p.ID, v1.Version) // stable -> live
	require.NoError(t, err)

	err = store.SetTag(ctx, p.ID, v1.Version, "prod")
	require.NoError(t, err)
	err = store.SetTag(ctx, p.ID, v2.Version, "dev")
	require.NoError(t, err)

	// 1. List with empty filter
	versions, err := store.ListVersions(ctx, p.ID, store.VersionFilter{})
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, 2, versions[0].Version)
	assert.Equal(t, 1, versions[1].Version)

	// 2. List filtering by Status = "live"
	liveStatus := "live"
	versions, err = store.ListVersions(ctx, p.ID, store.VersionFilter{Status: &liveStatus})
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, 1, versions[0].Version)

	// 3. List filtering by Status = "draft"
	draftStatus := "draft"
	versions, err = store.ListVersions(ctx, p.ID, store.VersionFilter{Status: &draftStatus})
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, 2, versions[0].Version)

	// 4. List filtering by Tag "dev"
	versions, err = store.ListVersions(ctx, p.ID, store.VersionFilter{Tags: []string{"dev"}})
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, 2, versions[0].Version)

	// 5. List filtering by Tag "prod"
	versions, err = store.ListVersions(ctx, p.ID, store.VersionFilter{Tags: []string{"prod"}})
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, 1, versions[0].Version)

	// 6. List filtering by non-existent tag
	versions, err = store.ListVersions(ctx, p.ID, store.VersionFilter{Tags: []string{"nonexistent"}})
	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestGetVersion(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "my template") //nolint:errcheck

	v, err := store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "my template", v.Template)
	assert.Equal(t, model.VersionStatusDraft, v.Status)
}

func TestGetVersion_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	_, err := store.GetVersion(ctx, p.ID, 99)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestGetLiveVersion(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "live template") //nolint:errcheck
	store.PromoteVersion(ctx, p.ID, 1)              // draft -> stable
	store.PromoteVersion(ctx, p.ID, 1)              // stable -> live

	v, err := store.GetLiveVersion(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusLive, v.Status)
	assert.Equal(t, "live template", v.Template)
}

func TestGetLiveVersion_NonePublished(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "draft template") //nolint:errcheck

	_, err := store.GetLiveVersion(ctx, p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestUpdateVersionTemplate(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	v, _ := store.CreateVersion(ctx, p.ID, "original")

	updated, err := store.UpdateVersionTemplate(ctx, v.ID, "updated")
	require.NoError(t, err)
	assert.Equal(t, "updated", updated.Template)
	assert.Equal(t, model.VersionStatusDraft, updated.Status)
}

func TestUpdateVersionTemplate_NonDraftRejected(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	v, _ := store.CreateVersion(ctx, p.ID, "original")
	store.PromoteVersion(ctx, p.ID, 1) // draft -> stable

	// UpdateVersionTemplate filters by status='draft', so it finds no row.
	_, err := store.UpdateVersionTemplate(ctx, v.ID, "new content")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestPromoteVersion_Lifecycle(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "template") //nolint:errcheck

	// Promote 1: draft -> stable
	v, err := store.PromoteVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusStable, v.Status)
	assert.NotNil(t, v.PublishedAt)

	// Promote 2: stable -> live
	v, err = store.PromoteVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusLive, v.Status)

	// Try to promote again (already live) -> ErrConflict
	_, err = store.PromoteVersion(ctx, p.ID, 1)
	assert.ErrorIs(t, err, store.ErrConflict)
}

func TestPromoteVersion_DemotesPreviousLive(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	store.CreateVersion(ctx, p.ID, "v1") //nolint:errcheck
	store.PromoteVersion(ctx, p.ID, 1)   // draft -> stable
	store.PromoteVersion(ctx, p.ID, 1)   // stable -> live

	store.CreateVersion(ctx, p.ID, "v2") //nolint:errcheck
	store.PromoteVersion(ctx, p.ID, 2)   // draft -> stable
	store.PromoteVersion(ctx, p.ID, 2)   // stable -> live

	v1, err := store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusStable, v1.Status) // v1 is demoted to stable

	v2, err := store.GetVersion(ctx, p.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusLive, v2.Status)
}

func TestPromoteVersion_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	_, err := store.PromoteVersion(ctx, p.ID, 99)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDemoteVersion_Success(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "v1")
	store.PromoteVersion(ctx, p.ID, 1) // draft -> stable
	store.PromoteVersion(ctx, p.ID, 1) // stable -> live

	v, err := store.DemoteVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusStable, v.Status)

	// Demoting again should fail since it's no longer live
	_, err = store.DemoteVersion(ctx, p.ID, 1)
	assert.ErrorIs(t, err, store.ErrConflict)
}

func TestArchiveVersion_Success(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "v1")

	// Archive directly from draft (allowed based on non-archived status)
	v, err := store.ArchiveVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusArchived, v.Status)

	// Archive again should fail
	_, err = store.ArchiveVersion(ctx, p.ID, 1)
	assert.ErrorIs(t, err, store.ErrConflict)
}

func TestDeleteVersion_Success(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v, err := store.CreateVersion(ctx, p.ID, "draft to delete")
	require.NoError(t, err)

	err = store.DeleteVersion(ctx, p.ID, v.Version)
	require.NoError(t, err)

	_, err = store.GetVersion(ctx, p.ID, v.Version)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteVersion_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	err := store.DeleteVersion(ctx, p.ID, 99)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteVersion_Unified(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	store.CreateVersion(ctx, p.ID, "v1")
	store.PromoteVersion(ctx, p.ID, 1) // draft -> stable
	store.PromoteVersion(ctx, p.ID, 1) // stable -> live

	err := store.DeleteVersion(ctx, p.ID, 1) // live version
	require.NoError(t, err)

	v1, err := store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusArchived, v1.Status) // soft-archived!

	store.CreateVersion(ctx, p.ID, "v2")
	store.PromoteVersion(ctx, p.ID, 2) // draft -> stable

	err = store.DeleteVersion(ctx, p.ID, 2) // stable version
	require.NoError(t, err)

	v2, err := store.GetVersion(ctx, p.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusArchived, v2.Status) // soft-archived!
}
