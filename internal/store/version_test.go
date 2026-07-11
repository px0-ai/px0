package store_test

import (
	"context"
	"encoding/json"
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

	v, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "template content"})
	require.NoError(t, err)
	assert.Equal(t, p.ID, v.PromptID)
	assert.Equal(t, 1, v.Version)
	assert.Equal(t, "template content", v.Template)
	assert.Equal(t, model.VersionStatusDraft, v.Status)
	assert.NotNil(t, v.CreatedAt)
	assert.Nil(t, v.PublishedAt)

	v2, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v2 template"})
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Version)
}

func TestCreateVersionWithModelConfig(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	versionModel := "gpt-4.1"
	modelParams := json.RawMessage(`{"temperature":0.2,"response_format":{"type":"json_object"}}`)
	v, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{
		Template:    "template content",
		Model:       &versionModel,
		ModelParams: modelParams,
	})
	require.NoError(t, err)
	require.NotNil(t, v.Model)
	assert.Equal(t, versionModel, *v.Model)
	assert.JSONEq(t, string(modelParams), string(v.ModelParams))

	got, err := store.GetVersion(ctx, p.ID, v.Version)
	require.NoError(t, err)
	require.NotNil(t, got.Model)
	assert.Equal(t, versionModel, *got.Model)
	assert.JSONEq(t, string(modelParams), string(got.ModelParams))
}

func TestListVersions(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v1, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v1"})
	require.NoError(t, err)
	v2, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v2"})
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
	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "my template"}) //nolint:errcheck

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
	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "live template"}) //nolint:errcheck
	store.PromoteVersion(ctx, p.ID, 1)                                                   // draft -> stable
	store.PromoteVersion(ctx, p.ID, 1)                                                   // stable -> live

	v, err := store.GetLiveVersion(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusLive, v.Status)
	assert.Equal(t, "live template", v.Template)
}

func TestGetLiveVersion_NonePublished(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "draft template"}) //nolint:errcheck

	_, err := store.GetLiveVersion(ctx, p.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestUpdateVersion(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	v, _ := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "original"})

	template := "updated"
	updated, err := store.UpdateVersion(ctx, v.ID, store.UpdateVersionParams{Template: &template})
	require.NoError(t, err)
	assert.Equal(t, "updated", updated.Template)
	assert.Equal(t, model.VersionStatusDraft, updated.Status)
}

func TestUpdateVersionModel(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	v, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "original"})
	require.NoError(t, err)

	versionModel := "gpt-4.1-mini"
	modelParams := json.RawMessage(`{"temperature":0.5}`)
	updated, err := store.UpdateVersion(ctx, v.ID, store.UpdateVersionParams{
		Model:       &versionModel,
		ModelParams: modelParams,
	})
	require.NoError(t, err)
	assert.Equal(t, "original", updated.Template)
	require.NotNil(t, updated.Model)
	assert.Equal(t, versionModel, *updated.Model)
	assert.JSONEq(t, string(modelParams), string(updated.ModelParams))
}

func TestDuplicateVersionCopiesModelConfig(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	versionModel := "gpt-4.1"
	modelParams := json.RawMessage(`{"temperature":0.2}`)
	_, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{
		Template:    "original",
		Model:       &versionModel,
		ModelParams: modelParams,
	})
	require.NoError(t, err)

	duplicated, err := store.DuplicateVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "original", duplicated.Template)
	require.NotNil(t, duplicated.Model)
	assert.Equal(t, versionModel, *duplicated.Model)
	assert.JSONEq(t, string(modelParams), string(duplicated.ModelParams))
}

func TestUpdateVersion_NonDraftRejected(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	v, _ := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "original"})
	store.PromoteVersion(ctx, p.ID, 1) // draft -> stable

	// UpdateVersion filters by status='draft', so it finds no row.
	template := "new content"
	_, err := store.UpdateVersion(ctx, v.ID, store.UpdateVersionParams{Template: &template})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestPromoteVersion_Lifecycle(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "template"}) //nolint:errcheck

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

	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v1"}) //nolint:errcheck
	store.PromoteVersion(ctx, p.ID, 1)                                        // draft -> stable
	store.PromoteVersion(ctx, p.ID, 1)                                        // stable -> live

	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v2"}) //nolint:errcheck
	store.PromoteVersion(ctx, p.ID, 2)                                        // draft -> stable
	store.PromoteVersion(ctx, p.ID, 2)                                        // stable -> live

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
	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v1"})
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
	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v1"})

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

	v, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "draft to delete"})
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

	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v1"})
	store.PromoteVersion(ctx, p.ID, 1) // draft -> stable
	store.PromoteVersion(ctx, p.ID, 1) // stable -> live

	err := store.DeleteVersion(ctx, p.ID, 1) // live version
	require.NoError(t, err)

	v1, err := store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusArchived, v1.Status) // soft-archived!

	store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v2"})
	store.PromoteVersion(ctx, p.ID, 2) // draft -> stable

	err = store.DeleteVersion(ctx, p.ID, 2) // stable version
	require.NoError(t, err)

	v2, err := store.GetVersion(ctx, p.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusArchived, v2.Status) // soft-archived!
}

func TestDuplicateVersion(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v1, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "original template content"})
	require.NoError(t, err)

	// Set a tag on v1 to ensure tag is NOT copied
	err = store.SetTag(ctx, p.ID, v1.Version, "v1.0")
	require.NoError(t, err)

	// Duplicate version 1
	v2, err := store.DuplicateVersion(ctx, p.ID, v1.Version)
	require.NoError(t, err)

	assert.Equal(t, p.ID, v2.PromptID)
	assert.Equal(t, 2, v2.Version)
	assert.Equal(t, "original template content", v2.Template)
	assert.Equal(t, model.VersionStatusDraft, v2.Status)
	assert.Empty(t, v2.Tags)
	assert.NotNil(t, v2.CreatedAt)
	assert.Nil(t, v2.PublishedAt)

	// Verify v1 is unchanged and still has its tag
	v1Updated, err := store.GetVersion(ctx, p.ID, v1.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{"v1.0"}, v1Updated.Tags)

	// Duplicate a non-existent version
	_, err = store.DuplicateVersion(ctx, p.ID, 999)
	assert.ErrorIs(t, err, store.ErrNotFound)
}
