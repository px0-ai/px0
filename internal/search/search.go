package search

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/model"
)

const (
	DefaultLimit = 20
	rrfConstant  = 60
)

// Request is the authorization-scoped input supplied to every retriever.
// Providers must honor ProjectIDs and Types before returning a match; the
// handler applies the project scope again while hydrating results.
type Request struct {
	Text       string
	ProjectIDs []uuid.UUID
	Types      []model.SearchEntityType
	Limit      int
}

// Match identifies a registry entity and its provider-specific relevance score.
// Retrievers must return matches ordered from most to least relevant.
type Match struct {
	Reference model.SearchReference
	Score     float64
}

// Retriever is implemented by lexical and semantic search backends. Keeping
// the input as natural-language text lets each semantic provider own its
// embedding strategy instead of exposing vectors through the public API.
type Retriever interface {
	Retrieve(context.Context, Request) ([]Match, error)
}

// Searcher is the handler-facing hybrid search contract.
type Searcher interface {
	Search(context.Context, Request) ([]model.SearchReference, error)
}

// Engine retrieves lexical and semantic candidates concurrently and reranks
// them with reciprocal-rank fusion, which is stable across incomparable score
// scales used by different providers.
type Engine struct {
	lexical  Retriever
	semantic Retriever
}

func NewEngine(lexical, semantic Retriever) *Engine {
	if lexical == nil {
		lexical = NoopRetriever{}
	}
	if semantic == nil {
		semantic = NoopRetriever{}
	}
	return &Engine{lexical: lexical, semantic: semantic}
}

func NewDefault() *Engine {
	return NewEngine(PostgresRetriever{}, NoopRetriever{})
}

func (e *Engine) Search(ctx context.Context, req Request) ([]model.SearchReference, error) {
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || len(req.ProjectIDs) == 0 {
		return []model.SearchReference{}, nil
	}
	if len(req.Types) == 0 {
		req.Types = model.AllSearchEntityTypes()
	}
	if req.Limit <= 0 {
		req.Limit = DefaultLimit
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type response struct {
		kind    string
		matches []Match
		err     error
	}
	responses := make(chan response, 2)

	go func() {
		matches, err := e.lexical.Retrieve(ctx, req)
		responses <- response{kind: "lexical", matches: matches, err: err}
	}()
	go func() {
		matches, err := e.semantic.Retrieve(ctx, req)
		responses <- response{kind: "semantic", matches: matches, err: err}
	}()

	var lexical, semantic []Match
	for range 2 {
		result := <-responses
		if result.err != nil {
			return nil, fmt.Errorf("%s retrieval: %w", result.kind, result.err)
		}
		if result.kind == "lexical" {
			lexical = result.matches
		} else {
			semantic = result.matches
		}
	}

	return fuse(lexical, semantic, req.Limit), nil
}

type fusedMatch struct {
	reference model.SearchReference
	score     float64
}

func fuse(lexical, semantic []Match, limit int) []model.SearchReference {
	scores := make(map[model.SearchReference]float64)

	for _, matches := range [][]Match{lexical, semantic} {
		seen := make(map[model.SearchReference]struct{}, len(matches))
		for rank, match := range matches {
			if match.Reference.ID == uuid.Nil {
				continue
			}
			if _, duplicate := seen[match.Reference]; duplicate {
				continue
			}
			seen[match.Reference] = struct{}{}
			scores[match.Reference] += 1 / float64(rrfConstant+rank+1)
		}
	}

	ranked := make([]fusedMatch, 0, len(scores))
	for reference, score := range scores {
		ranked = append(ranked, fusedMatch{reference: reference, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			if ranked[i].reference.Type == ranked[j].reference.Type {
				return ranked[i].reference.ID.String() < ranked[j].reference.ID.String()
			}
			return ranked[i].reference.Type < ranked[j].reference.Type
		}
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	references := make([]model.SearchReference, len(ranked))
	for i, match := range ranked {
		references[i] = match.reference
	}
	return references
}
