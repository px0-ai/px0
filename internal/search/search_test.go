package search

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestEngineSearchRetrievesAndReranksBothSources(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	b := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	c := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	projectID := uuid.New()
	lexical := &fakeRetriever{matches: []Match{{PromptID: a, Score: 0.9}, {PromptID: b, Score: 0.8}}}
	semantic := &fakeRetriever{matches: []Match{{PromptID: b, Score: 0.7}, {PromptID: c, Score: 0.6}}}

	ids, err := NewEngine(lexical, semantic).Search(context.Background(), Request{
		Text:       "  refund policy  ",
		ProjectIDs: []uuid.UUID{projectID},
		Status:     "active",
		Limit:      3,
	})

	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{b, a, c}, ids)
	require.Len(t, lexical.calls, 1)
	require.Len(t, semantic.calls, 1)
	assert.Equal(t, "refund policy", lexical.calls[0].Text)
	assert.Equal(t, []uuid.UUID{projectID}, semantic.calls[0].ProjectIDs)
}

func TestEngineSearchRejectsRetrieverFailure(t *testing.T) {
	lexical := &fakeRetriever{err: errors.New("database unavailable")}
	semantic := &fakeRetriever{}

	_, err := NewEngine(lexical, semantic).Search(context.Background(), Request{
		Text:       "refund",
		ProjectIDs: []uuid.UUID{uuid.New()},
		Status:     "active",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "lexical retrieval")
}

func TestEngineSearchSkipsRetrieversWithoutQueryOrScope(t *testing.T) {
	lexical := &fakeRetriever{}
	semantic := &fakeRetriever{}
	engine := NewEngine(lexical, semantic)

	ids, err := engine.Search(context.Background(), Request{Text: "   ", ProjectIDs: []uuid.UUID{uuid.New()}})
	require.NoError(t, err)
	assert.Empty(t, ids)

	ids, err = engine.Search(context.Background(), Request{Text: "refund"})
	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.Zero(t, lexical.callCount())
	assert.Zero(t, semantic.callCount())
}

func TestFuseDeduplicatesProviderResultsAndAppliesLimit(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	b := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	ids := fuse(
		[]Match{{PromptID: a}, {PromptID: a}, {PromptID: b}, {PromptID: uuid.Nil}},
		[]Match{{PromptID: b}},
		1,
	)

	assert.Equal(t, []uuid.UUID{b}, ids)
}

func TestNewFromEnvValidatesConfiguredProviders(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "postgres")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "none")
	engine, err := NewFromEnv()
	require.NoError(t, err)
	require.NotNil(t, engine)

	t.Setenv("SEARCH_VECTOR_PROVIDER", "qdrant")
	_, err = NewFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")

	t.Setenv("SEARCH_VECTOR_PROVIDER", "unknown")
	_, err = NewFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown vector search provider")
}
