package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/search"
)

type mockProvider struct {
	IndexCalls   []search.IndexablePrompt
	DeindexCalls []uuid.UUID
}

func (m *mockProvider) Search(ctx context.Context, q search.SearchQuery) ([]search.SearchResult, error) {
	return nil, nil
}

func (m *mockProvider) Index(ctx context.Context, p search.IndexablePrompt) error {
	m.IndexCalls = append(m.IndexCalls, p)
	return nil
}

func (m *mockProvider) Deindex(ctx context.Context, promptID uuid.UUID) error {
	m.DeindexCalls = append(m.DeindexCalls, promptID)
	return nil
}

func TestPromptMutations_SearchTriggers(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Inject the mock provider and restore it after
	originalProvider := search.Get()
	mock := &mockProvider{}
	search.Init(mock)
	t.Cleanup(func() { search.Init(originalProvider) })

	// 1. Create a prompt (should trigger Index)
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Trigger Test","description":"Initial desc","slug":"trigger_test"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	
	body := decodeBody(t, resp)
	promptMap := body["prompt"].(map[string]any)
	promptIDStr := promptMap["id"].(string)
	promptID := uuid.MustParse(promptIDStr)

	require.Len(t, mock.IndexCalls, 1, "CreatePrompt should call Index once")
	assert.Equal(t, promptID, mock.IndexCalls[0].ID)
	assert.Equal(t, "Trigger Test", mock.IndexCalls[0].Name)
	assert.Equal(t, "Initial desc", mock.IndexCalls[0].Description)
	assert.Equal(t, "trigger_test", mock.IndexCalls[0].Slug)
	assert.Equal(t, model.PromptStatusActive, mock.IndexCalls[0].Status)
	assert.Empty(t, mock.IndexCalls[0].Tags) // No tags initially

	// Clear mock calls for the next step
	mock.IndexCalls = nil

	// 2. Update the prompt (should trigger Index)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s", promptID),
		`{"description":"Updated desc"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, mock.IndexCalls, 1, "UpdatePrompt should call Index once")
	assert.Equal(t, promptID, mock.IndexCalls[0].ID)
	assert.Equal(t, "Updated desc", mock.IndexCalls[0].Description)

	// Clear mock calls
	mock.IndexCalls = nil

	// 3. Create a version and tag it (should trigger Index to update tags)
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", promptID),
		`{"template":"Hello world"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/tags", promptID),
		`{"tag":"live"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, mock.IndexCalls, 1, "SetTag should call Index once")
	assert.Equal(t, promptID, mock.IndexCalls[0].ID)
	assert.Contains(t, mock.IndexCalls[0].Tags, "live")

	// Clear mock calls
	mock.IndexCalls = nil

	// 4. Archive the prompt (should trigger Deindex)
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, mock.DeindexCalls, 1, "ArchivePrompt should call Deindex once")
	assert.Equal(t, promptID, mock.DeindexCalls[0])
	require.Len(t, mock.IndexCalls, 0, "ArchivePrompt should not call Index")
}
