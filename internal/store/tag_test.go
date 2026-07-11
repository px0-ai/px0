package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestTags_StoreLifecycle(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	p := newPrompt(t, ctx)

	v1, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v1 template"})
	require.NoError(t, err)

	v2, err := store.CreateVersion(ctx, p.ID, store.CreateVersionParams{Template: "v2 template"})
	require.NoError(t, err)

	// 1. SetTag
	err = store.SetTag(ctx, p.ID, v1.Version, "prod")
	require.NoError(t, err)

	err = store.SetTag(ctx, p.ID, v1.Version, "stable")
	require.NoError(t, err)

	// 2. GetTagsForVersion
	tagsV1, err := store.GetTagsForVersion(ctx, p.ID, v1.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod", "stable"}, tagsV1)

	// 3. GetVersionByTag
	gotV1, err := store.GetVersionByTag(ctx, p.ID, "prod")
	require.NoError(t, err)
	assert.Equal(t, v1.Version, gotV1.Version)
	assert.Equal(t, []string{"prod", "stable"}, gotV1.Tags)

	// 4. GetVersion (automatically populates tags)
	gotV1Direct, err := store.GetVersion(ctx, p.ID, v1.Version)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod", "stable"}, gotV1Direct.Tags)

	// 5. GetTagsForPrompt
	tagMap, err := store.GetTagsForPrompt(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod", "stable"}, tagMap[v1.Version])
	assert.Nil(t, tagMap[v2.Version])

	// 6. Move/Upsert tag to v2
	err = store.SetTag(ctx, p.ID, v2.Version, "prod")
	require.NoError(t, err)

	// v1 should lose 'prod' but keep 'stable', and v2 should gain 'prod'
	tagsV1Updated, _ := store.GetTagsForVersion(ctx, p.ID, v1.Version)
	assert.Equal(t, []string{"stable"}, tagsV1Updated)

	tagsV2Updated, _ := store.GetTagsForVersion(ctx, p.ID, v2.Version)
	assert.Equal(t, []string{"prod"}, tagsV2Updated)

	// 7. RemoveTag
	err = store.RemoveTag(ctx, p.ID, "stable")
	require.NoError(t, err)

	tagsV1AfterRemove, _ := store.GetTagsForVersion(ctx, p.ID, v1.Version)
	assert.Equal(t, []string{}, tagsV1AfterRemove)

	// 8. Remove non-existent tag
	err = store.RemoveTag(ctx, p.ID, "nonexistent")
	assert.ErrorIs(t, err, store.ErrNotFound)

	// 9. Foreign key violation (tagging non-existent version)
	err = store.SetTag(ctx, p.ID, 999, "beta")
	assert.ErrorIs(t, err, store.ErrNotFound)
}
