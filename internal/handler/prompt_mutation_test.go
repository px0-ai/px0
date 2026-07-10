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

type mockFTSProvider struct {
	SearchCalls   int
	IndexCalls    []search.IndexablePrompt
	DeindexCalls  []uuid.UUID
}

func (m *mockFTSProvider) Search(ctx context.Context, q search.SearchQuery) ([]search.SearchResult, error) {
	m.SearchCalls++
	return nil, nil
}

func (m *mockFTSProvider) Index(ctx context.Context, p search.IndexablePrompt) error {
	m.IndexCalls = append(m.IndexCalls, p)
	return nil
}

func (m *mockFTSProvider) Deindex(ctx context.Context, promptID uuid.UUID) error {
	m.DeindexCalls = append(m.DeindexCalls, promptID)
	return nil
}

type mockVectorProvider struct {
	SearchCalls  int
	IndexCalls   []search.IndexablePrompt
	DeindexCalls []uuid.UUID
}

func (m *mockVectorProvider) Search(ctx context.Context, q search.SearchQuery) ([]search.SearchResult, error) {
	m.SearchCalls++
	return nil, nil
}

func (m *mockVectorProvider) Index(ctx context.Context, p search.IndexablePrompt) error {
	m.IndexCalls = append(m.IndexCalls, p)
	return nil
}

func (m *mockVectorProvider) Deindex(ctx context.Context, promptID uuid.UUID) error {
	m.DeindexCalls = append(m.DeindexCalls, promptID)
	return nil
}

func TestPromptMutations_SearchTriggers(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Inject mock providers and restore the originals after.
	originalFTS := search.GetFTS()
	originalVector := search.GetVector()
	mockFTS := &mockFTSProvider{}
	mockVector := &mockVectorProvider{}
	search.Init(mockFTS, mockVector)
	t.Cleanup(func() { search.Init(originalFTS, originalVector) })

	// 1. Create a prompt (should trigger Index on the vector provider,
	// since that's the one wrapped in the embedding decorator).
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Trigger Test","description":"Initial desc","slug":"trigger_test"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	promptMap := body["prompt"].(map[string]any)
	promptIDStr := promptMap["id"].(string)
	promptID := uuid.MustParse(promptIDStr)

	require.Len(t, mockVector.IndexCalls, 1, "CreatePrompt should call Index on the vector provider once")
	assert.Equal(t, promptID, mockVector.IndexCalls[0].ID)
	assert.Equal(t, "Trigger Test", mockVector.IndexCalls[0].Name)
	assert.Equal(t, "Initial desc", mockVector.IndexCalls[0].Description)
	assert.Equal(t, "trigger_test", mockVector.IndexCalls[0].Slug)
	assert.Equal(t, model.PromptStatusActive, mockVector.IndexCalls[0].Status)
	assert.Empty(t, mockVector.IndexCalls[0].Tags) // No tags initially

	// FTS Index should NOT be called for plain FTS providers
	// (postgres is a no-op; qdrant would not receive the index either
	// since the handler routes Index through the vector slot only).
	assert.Empty(t, mockFTS.IndexCalls, "FTS provider's Index should not be called")

	// Clear mock calls for the next step
	mockVector.IndexCalls = nil

	// 2. Update the prompt (should trigger Index)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s", promptID),
		`{"description":"Updated desc"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, mockVector.IndexCalls, 1, "UpdatePrompt should call Index once")
	assert.Equal(t, promptID, mockVector.IndexCalls[0].ID)
	assert.Equal(t, "Updated desc", mockVector.IndexCalls[0].Description)

	// Clear mock calls
	mockVector.IndexCalls = nil

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

	require.Len(t, mockVector.IndexCalls, 1, "SetTag should call Index once")
	assert.Equal(t, promptID, mockVector.IndexCalls[0].ID)
	assert.Contains(t, mockVector.IndexCalls[0].Tags, "live")

	// Clear mock calls
	mockVector.IndexCalls = nil

	// 4. Archive the prompt (should trigger Deindex on the vector provider)
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	require.Len(t, mockVector.DeindexCalls, 1, "ArchivePrompt should call Deindex on the vector provider once")
	assert.Equal(t, promptID, mockVector.DeindexCalls[0])
	require.Len(t, mockVector.IndexCalls, 0, "ArchivePrompt should not call Index")
}
