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
	t.Helper()
	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)
	p, err := store.CreatePrompt(ctx, tm.ID, "test_prompt", "Test Prompt", "")
	require.NoError(t, err)
	return p
}

func TestCreateVersion(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v, err := store.CreateVersion(ctx, p.ID, "Hello, {{.name}}!")
	require.NoError(t, err)
	assert.Equal(t, 1, v.Version)
	assert.Equal(t, model.VersionStatusDraft, v.Status)
	assert.Equal(t, "Hello, {{.name}}!", v.Template)
	assert.Nil(t, v.PublishedAt)
}

func TestCreateVersion_AutoIncrements(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v1, err := store.CreateVersion(ctx, p.ID, "v1 template")
	require.NoError(t, err)
	assert.Equal(t, 1, v1.Version)

	v2, err := store.CreateVersion(ctx, p.ID, "v2 template")
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Version)

	v3, err := store.CreateVersion(ctx, p.ID, "v3 template")
	require.NoError(t, err)
	assert.Equal(t, 3, v3.Version)
}

func TestCreateVersion_PerPrompt(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	p1 := newPrompt(t, ctx)
	p2 := newPrompt(t, ctx)

	v1, _ := store.CreateVersion(ctx, p1.ID, "p1 template")
	v2, _ := store.CreateVersion(ctx, p2.ID, "p2 template")

	// Each prompt gets its own version counter starting at 1.
	assert.Equal(t, 1, v1.Version)
	assert.Equal(t, 1, v2.Version)
}

func TestListVersions(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	store.CreateVersion(ctx, p.ID, "v1") //nolint:errcheck
	store.CreateVersion(ctx, p.ID, "v2") //nolint:errcheck

	versions, err := store.ListVersions(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	// Returned newest first.
	assert.Equal(t, 2, versions[0].Version)
	assert.Equal(t, 1, versions[1].Version)
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
	store.PublishVersion(ctx, p.ID, 1)              //nolint:errcheck

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

func TestUpdateVersionTemplate_LiveVersionRejected(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	v, _ := store.CreateVersion(ctx, p.ID, "original")
	store.PublishVersion(ctx, p.ID, 1) //nolint:errcheck

	// UpdateVersionTemplate filters by status='draft', so it finds no row.
	_, err := store.UpdateVersionTemplate(ctx, v.ID, "new content")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestPublishVersion_FirstVersion(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "template") //nolint:errcheck

	v, err := store.PublishVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusLive, v.Status)
	assert.NotNil(t, v.PublishedAt)
}

func TestPublishVersion_ArchivesPreviousLive(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	store.CreateVersion(ctx, p.ID, "v1") //nolint:errcheck
	store.PublishVersion(ctx, p.ID, 1)   //nolint:errcheck
	store.CreateVersion(ctx, p.ID, "v2") //nolint:errcheck
	store.PublishVersion(ctx, p.ID, 2)   //nolint:errcheck

	v1, err := store.GetVersion(ctx, p.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusArchived, v1.Status)

	v2, err := store.GetVersion(ctx, p.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, model.VersionStatusLive, v2.Status)
}

func TestPublishVersion_NotFound(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	_, err := store.PublishVersion(ctx, p.ID, 99)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestPublishVersion_AlreadyLive(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)
	store.CreateVersion(ctx, p.ID, "template") //nolint:errcheck
	store.PublishVersion(ctx, p.ID, 1)         //nolint:errcheck

	_, err := store.PublishVersion(ctx, p.ID, 1)
	assert.ErrorIs(t, err, store.ErrConflict)
}

func TestPublishVersion_AlreadyArchived(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	store.CreateVersion(ctx, p.ID, "v1")
	store.PublishVersion(ctx, p.ID, 1)
	store.CreateVersion(ctx, p.ID, "v2")
	store.PublishVersion(ctx, p.ID, 2) // v1 is now archived

	_, err := store.PublishVersion(ctx, p.ID, 1) // try to re-publish archived
	assert.ErrorIs(t, err, store.ErrConflict)
}
