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

func TestPostgresRetrieverSearchesFieldsAndEnforcesScope(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()

	teamA, err := store.CreateTeam(ctx, "Search Team A")
	require.NoError(t, err)
	projectA, err := store.CreateProject(ctx, teamA.ID, "search-a", "Search A")
	require.NoError(t, err)
	nameMatch, err := store.CreatePrompt(ctx, projectA.ID, "returns", "Refund Assistant", "Handles support")
	require.NoError(t, err)
	descriptionMatch, err := store.CreatePrompt(ctx, projectA.ID, "support", "Support Assistant", "Explains refund policies")
	require.NoError(t, err)

	teamB, err := store.CreateTeam(ctx, "Search Team B")
	require.NoError(t, err)
	projectB, err := store.CreateProject(ctx, teamB.ID, "search-b", "Search B")
	require.NoError(t, err)
	_, err = store.CreatePrompt(ctx, projectB.ID, "private", "Refund Private", "Must not leak")
	require.NoError(t, err)

	retriever := search.PostgresRetriever{}
	matches, err := retriever.Retrieve(ctx, search.Request{
		Text:       "refund",
		ProjectIDs: []uuid.UUID{projectA.ID},
		Status:     model.PromptStatusActive,
		Limit:      10,
	})
	require.NoError(t, err)
	require.Len(t, matches, 2)
	assert.Equal(t, nameMatch.ID, matches[0].PromptID)
	assert.Equal(t, descriptionMatch.ID, matches[1].PromptID)
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
		Status:     model.PromptStatusActive,
		Limit:      10,
	})
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestPostgresRetrieverReflectsPromptUpdates(t *testing.T) {
	testutil.SetupDB(t)
	ctx := context.Background()
	team, err := store.CreateTeam(ctx, "Updated Search Team")
	require.NoError(t, err)
	project, err := store.CreateProject(ctx, team.ID, "updated-search", "Updated Search")
	require.NoError(t, err)
	prompt, err := store.CreatePrompt(ctx, project.ID, "assistant", "Support Assistant", "Handles billing")
	require.NoError(t, err)

	_, err = store.UpdatePrompt(ctx, prompt.ID, []uuid.UUID{project.ID}, "Handles chargebacks")
	require.NoError(t, err)
	retriever := search.PostgresRetriever{}
	request := search.Request{
		ProjectIDs: []uuid.UUID{project.ID},
		Status:     model.PromptStatusActive,
		Limit:      10,
	}

	request.Text = "chargebacks"
	matches, err := retriever.Retrieve(ctx, request)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, prompt.ID, matches[0].PromptID)

	request.Text = "billing"
	matches, err = retriever.Retrieve(ctx, request)
	require.NoError(t, err)
	assert.Empty(t, matches)
}
