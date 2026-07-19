package search

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/model"
)

type fakeRetriever struct {
	mu      sync.Mutex
	matches []Match
	err     error
	calls   []Request
}

func (f *fakeRetriever) Retrieve(_ context.Context, req Request) ([]Match, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.matches, f.err
}

func (f *fakeRetriever) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func reference(entityType model.SearchEntityType, id string) model.SearchReference {
	return model.SearchReference{Type: entityType, ID: uuid.MustParse(id)}
}

func TestEngineSearchRetrievesAndReranksBothSources(t *testing.T) {
	a := reference(model.SearchEntityPrompt, "00000000-0000-0000-0000-000000000001")
	b := reference(model.SearchEntitySkill, "00000000-0000-0000-0000-000000000002")
	c := reference(model.SearchEntityTool, "00000000-0000-0000-0000-000000000003")
	projectID := uuid.New()
	lexical := &fakeRetriever{matches: []Match{{Reference: a, Score: 0.9}, {Reference: b, Score: 0.8}}}
	semantic := &fakeRetriever{matches: []Match{{Reference: b, Score: 0.7}, {Reference: c, Score: 0.6}}}

	references, err := NewEngine(lexical, semantic).Search(context.Background(), Request{
		Text:       "  refund policy  ",
		ProjectIDs: []uuid.UUID{projectID},
		Types:      model.AllSearchEntityTypes(),
		Limit:      3,
	})

	require.NoError(t, err)
	assert.Equal(t, []model.SearchReference{b, a, c}, references)
	require.Len(t, lexical.calls, 1)
	require.Len(t, semantic.calls, 1)
	assert.Equal(t, "refund policy", lexical.calls[0].Text)
	assert.Equal(t, []uuid.UUID{projectID}, semantic.calls[0].ProjectIDs)
	assert.Equal(t, model.AllSearchEntityTypes(), semantic.calls[0].Types)
}

func TestEngineSearchDefaultsToAllEntityTypes(t *testing.T) {
	lexical := &fakeRetriever{}
	semantic := &fakeRetriever{}

	_, err := NewEngine(lexical, semantic).Search(context.Background(), Request{
		Text:       "refund",
		ProjectIDs: []uuid.UUID{uuid.New()},
	})

	require.NoError(t, err)
	require.Len(t, lexical.calls, 1)
	assert.Equal(t, model.AllSearchEntityTypes(), lexical.calls[0].Types)
}

func TestEngineSearchRejectsRetrieverFailure(t *testing.T) {
	lexical := &fakeRetriever{err: errors.New("database unavailable")}
	semantic := &fakeRetriever{}

	_, err := NewEngine(lexical, semantic).Search(context.Background(), Request{
		Text:       "refund",
		ProjectIDs: []uuid.UUID{uuid.New()},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "lexical retrieval")
}

func TestEngineSearchSkipsRetrieversWithoutQueryOrScope(t *testing.T) {
	lexical := &fakeRetriever{}
	semantic := &fakeRetriever{}
	engine := NewEngine(lexical, semantic)

	references, err := engine.Search(context.Background(), Request{Text: "   ", ProjectIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.Empty(t, references)

	references, err = engine.Search(context.Background(), Request{Text: "refund"})
	require.NoError(t, err)
	assert.Empty(t, references)
	assert.Zero(t, lexical.callCount())
	assert.Zero(t, semantic.callCount())
}

func TestFuseDeduplicatesProviderResultsAndAppliesLimit(t *testing.T) {
	a := reference(model.SearchEntityPrompt, "00000000-0000-0000-0000-000000000001")
	b := reference(model.SearchEntityTool, "00000000-0000-0000-0000-000000000002")

	references := fuse(
		[]Match{{Reference: a}, {Reference: a}, {Reference: b}, {Reference: model.SearchReference{Type: model.SearchEntitySkill}}},
		[]Match{{Reference: b}},
		1,
	)

	assert.Equal(t, []model.SearchReference{b}, references)
}

func TestNewFromEnvValidatesConfiguredProviders(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "postgres")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "none")
	engine, err := NewFromEnv()
	require.NoError(t, err)
	require.NotNil(t, engine)

	t.Setenv("SEARCH_VECTOR_PROVIDER", "unknown")
	_, err = NewFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown vector search provider")

	t.Setenv("SEARCH_FTS_PROVIDER", "unknown")
	_, err = NewFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown FTS search provider")
}
