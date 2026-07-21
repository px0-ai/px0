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

func TestCreateAndGetSkill(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "skills_project", "Skills Project")
	require.NoError(t, err)

	s, err := store.CreateSkill(ctx, p.ID, "my-skill", "My Skill", "A test skill")
	require.NoError(t, err)
	assert.NotEmpty(t, s.ID)
	assert.Equal(t, p.ID, s.ProjectID)
	assert.Equal(t, "my-skill", s.Slug)
	assert.Equal(t, "My Skill", s.Name)
	assert.Equal(t, "A test skill", s.Description)

	// Retrieve skill by ID
	s2, err := store.GetSkillByID(ctx, s.ID, []uuid.UUID{p.ID})
	require.NoError(t, err)
	assert.Equal(t, s.ID, s2.ID)

	// Retrieve skill by slug
	s3, err := store.GetSkillBySlug(ctx, "my-skill", []uuid.UUID{p.ID})
	require.NoError(t, err)
	assert.Equal(t, s.ID, s3.ID)

	// Get with unauthorized project ID should return NotFound
	_, err = store.GetSkillByID(ctx, s.ID, []uuid.UUID{uuid.New()})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestCreateSkill_Duplicate(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "skills_project", "Skills Project")
	require.NoError(t, err)

	_, err = store.CreateSkill(ctx, p.ID, "my-skill", "My Skill", "A test skill")
	require.NoError(t, err)

	// Duplicate slug
	_, err = store.CreateSkill(ctx, p.ID, "my-skill", "Other Skill", "")
	assert.ErrorIs(t, err, store.ErrDuplicate)

	// Duplicate name
	_, err = store.CreateSkill(ctx, p.ID, "other-skill", "My Skill", "")
	assert.ErrorIs(t, err, store.ErrDuplicate)
}

func TestUpdateAndDeleteSkill(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "skills_project", "Skills Project")
	require.NoError(t, err)

	s, err := store.CreateSkill(ctx, p.ID, "my-skill", "My Skill", "A test skill")
	require.NoError(t, err)

	// Update metadata
	updated, err := store.UpdateSkill(ctx, s.ID, []uuid.UUID{p.ID}, "my-skill-updated", "My Skill Updated", "New Description")
	require.NoError(t, err)
	assert.Equal(t, "my-skill-updated", updated.Slug)
	assert.Equal(t, "My Skill Updated", updated.Name)
	assert.Equal(t, "New Description", updated.Description)

	// Delete
	err = store.DeleteSkill(ctx, s.ID, []uuid.UUID{p.ID})
	require.NoError(t, err)

	_, err = store.GetSkillByID(ctx, s.ID, []uuid.UUID{p.ID})
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestSkillVersions(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "skills_project", "Skills Project")
	require.NoError(t, err)

	s, err := store.CreateSkill(ctx, p.ID, "my-skill", "My Skill", "")
	require.NoError(t, err)

	// Assert version 1 was auto-created as draft
	v1, err := store.GetSkillVersion(ctx, s.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "draft", v1.Status)

	// Create a file in v1
	err = store.UpsertSkillFile(ctx, v1.ID, s.ID, "index.js", []byte("console.log('v1')"))
	require.NoError(t, err)

	// Duplicate version 1 to create version 2
	v2, err := store.DuplicateSkillVersion(ctx, s.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Version)
	assert.Equal(t, "draft", v2.Status)

	// Assert file was copied to v2
	fileV2, err := store.GetSkillFile(ctx, v2.ID, "index.js")
	require.NoError(t, err)
	assert.Equal(t, "console.log('v1')", string(fileV2.Content))

	// Promote v1 to stable
	v1Prom, err := store.PromoteSkillVersion(ctx, s.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "stable", v1Prom.Status)

	// Promote v1 again to live
	v1Live, err := store.PromoteSkillVersion(ctx, s.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "live", v1Live.Status)

	// Demote v1 back to stable
	v1Stable, err := store.DemoteSkillVersion(ctx, s.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, "stable", v1Stable.Status)

	// Create and delete version 3 as draft
	v3, err := store.CreateSkillVersion(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, v3.Version)

	err = store.DeleteSkillVersion(ctx, s.ID, 3)
	require.NoError(t, err)

	_, err = store.GetSkillVersion(ctx, s.ID, 3)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestSkillFiles(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	tm, err := store.CreateTeam(ctx, "Test Team")
	require.NoError(t, err)

	p, err := store.CreateProject(ctx, tm.ID, "skills_project", "Skills Project")
	require.NoError(t, err)

	s, err := store.CreateSkill(ctx, p.ID, "my-skill", "My Skill", "")
	require.NoError(t, err)

	v1, err := store.GetSkillVersion(ctx, s.ID, 1)
	require.NoError(t, err)

	// Create file
	err = store.UpsertSkillFile(ctx, v1.ID, s.ID, "src/main.go", []byte("package main"))
	require.NoError(t, err)

	// List files
	files, err := store.GetSkillFiles(ctx, v1.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "src/main.go", files[0].FilePath)
	assert.Equal(t, "package main", string(files[0].Content))

	// Replace files
	newFiles := []model.SkillFile{
		{FilePath: "index.js", Content: []byte("const a = 1;")},
		{FilePath: "package.json", Content: []byte("{}")},
	}
	err = store.ReplaceSkillFiles(ctx, v1.ID, s.ID, newFiles)
	require.NoError(t, err)

	files2, err := store.GetSkillFiles(ctx, v1.ID)
	require.NoError(t, err)
	require.Len(t, files2, 2)
	assert.Equal(t, "index.js", files2[0].FilePath)
	assert.Equal(t, "package.json", files2[1].FilePath)

	// Delete individual file
	err = store.DeleteSkillFile(ctx, v1.ID, "package.json")
	require.NoError(t, err)

	files3, err := store.GetSkillFiles(ctx, v1.ID)
	require.NoError(t, err)
	require.Len(t, files3, 1)
	assert.Equal(t, "index.js", files3[0].FilePath)
}
