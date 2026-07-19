package search_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/search"
	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

func TestPostgresRetrieverSearchesAllEntityTypesAndEnforcesScope(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	team, err := store.CreateTeam(ctx, "Search Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, team.ID, "search", "Search")
	require.NoError(t, err)
	prompt, err := store.CreatePrompt(ctx, project.ID, "refund-prompt", "Refund Prompt", "Handles support")
	require.NoError(t, err)
	skill, err := store.CreateSkill(ctx, project.ID, "refund-skill", "Refund Skill", "Handles support")
	require.NoError(t, err)
	tool, err := store.CreateTool(ctx, project.ID, "refund-tool", "Refund Tool", "Handles support", "")
	require.NoError(t, err)

	foreignTeam, err := store.CreateTeam(ctx, "Foreign Search Team")
	require.NoError(t, err)
	foreignProject, err := store.CreateProject(ctx, foreignTeam.ID, "foreign-search", "Foreign Search")
	require.NoError(t, err)
	_, err = store.CreateTool(ctx, foreignProject.ID, "private-refund", "Private Refund", "Must not leak", "")
	require.NoError(t, err)

	matches, err := (search.PostgresRetriever{}).Retrieve(ctx, search.Request{
		Text:       "refund",
		ProjectIDs: []uuid.UUID{project.ID},
		Types:      model.AllSearchEntityTypes(),
		Limit:      10,
	})
	require.NoError(t, err)
	require.Len(t, matches, 3)

	actual := make(map[model.SearchEntityType]uuid.UUID, len(matches))
	for _, match := range matches {
		actual[match.Reference.Type] = match.Reference.ID
	}
	assert.Equal(t, prompt.ID, actual[model.SearchEntityPrompt])
	assert.Equal(t, skill.ID, actual[model.SearchEntitySkill])
	assert.Equal(t, tool.ID, actual[model.SearchEntityTool])
}

func TestPostgresRetrieverHonorsEntityTypeFilter(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	team, err := store.CreateTeam(ctx, "Filtered Search Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, team.ID, "filtered-search", "Filtered Search")
	require.NoError(t, err)
	_, err = store.CreatePrompt(ctx, project.ID, "refund-prompt", "Refund Prompt", "")
	require.NoError(t, err)
	tool, err := store.CreateTool(ctx, project.ID, "refund-tool", "Refund Tool", "", "")
	require.NoError(t, err)

	matches, err := (search.PostgresRetriever{}).Retrieve(ctx, search.Request{
		Text:       "refund",
		ProjectIDs: []uuid.UUID{project.ID},
		Types:      []model.SearchEntityType{model.SearchEntityTool},
		Limit:      10,
	})
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, model.SearchReference{Type: model.SearchEntityTool, ID: tool.ID}, matches[0].Reference)
}

func TestPostgresRetrieverExcludesArchivedPrompts(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	team, err := store.CreateTeam(ctx, "Archive Search Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, team.ID, "archive-search", "Archive Search")
	require.NoError(t, err)
	prompt, err := store.CreatePrompt(ctx, project.ID, "refund", "Refund Assistant", "Handles refunds")
	require.NoError(t, err)
	require.NoError(t, store.ArchivePrompt(ctx, prompt.ID, []uuid.UUID{project.ID}))

	matches, err := (search.PostgresRetriever{}).Retrieve(ctx, search.Request{
		Text:       "refund",
		ProjectIDs: []uuid.UUID{project.ID},
		Types:      []model.SearchEntityType{model.SearchEntityPrompt},
		Limit:      10,
	})
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestPostgresRetrieverReflectsEntityUpdates(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	team, err := store.CreateTeam(ctx, "Updated Search Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, team.ID, "updated-search", "Updated Search")
	require.NoError(t, err)
	tool, err := store.CreateTool(ctx, project.ID, "assistant", "Support Assistant", "Handles billing", "")
	require.NoError(t, err)
	_, err = store.UpdateTool(ctx, tool.ID, []uuid.UUID{project.ID}, "assistant", "Support Assistant", "Handles chargebacks", "")
	require.NoError(t, err)

	request := search.Request{
		ProjectIDs: []uuid.UUID{project.ID},
		Types:      []model.SearchEntityType{model.SearchEntityTool},
		Limit:      10,
	}
	request.Text = "chargebacks"
	matches, err := (search.PostgresRetriever{}).Retrieve(ctx, request)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, tool.ID, matches[0].Reference.ID)

	request.Text = "billing"
	matches, err = (search.PostgresRetriever{}).Retrieve(ctx, request)
	require.NoError(t, err)
	assert.Empty(t, matches)
}
