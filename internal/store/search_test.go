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

func TestGetSearchResultsPreservesRankAndEnforcesScope(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	project := newProject(t, ctx, "Search Results")
	foreignProject := newProject(t, ctx, "Foreign Results")

	prompt, err := store.CreatePrompt(ctx, project.ID, "prompt", "Prompt", "")
	require.NoError(t, err)
	skill, err := store.CreateSkill(ctx, project.ID, "skill", "Skill", "")
	require.NoError(t, err)
	tool, err := store.CreateTool(ctx, project.ID, "tool", "Tool", "", "")
	require.NoError(t, err)
	foreign, err := store.CreateTool(ctx, foreignProject.ID, "foreign", "Foreign", "", "")
	require.NoError(t, err)
	require.NoError(t, store.ArchivePrompt(ctx, prompt.ID, []uuid.UUID{project.ID}))

	references := []model.SearchReference{
		{Type: model.SearchEntityTool, ID: tool.ID},
		{Type: model.SearchEntityTool, ID: foreign.ID},
		{Type: model.SearchEntityPrompt, ID: prompt.ID},
		{Type: model.SearchEntitySkill, ID: skill.ID},
	}
	results, err := store.GetSearchResults(ctx, references, []uuid.UUID{project.ID})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, tool.ID, results[0].ID)
	assert.Equal(t, model.SearchEntityTool, results[0].Type)
	assert.Equal(t, skill.ID, results[1].ID)
	assert.Equal(t, model.SearchEntitySkill, results[1].Type)
}
