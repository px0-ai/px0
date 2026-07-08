package search

import (
	"context"

	"errors"

	"github.com/google/uuid"
)

var ErrNotImplemented = errors.New("search provider not implemented")

// SearchQuery holds the parameters a caller can pass to search for prompts.
// All fields are optional — an empty Q with TeamIDs returns all accessible prompts.
type SearchQuery struct {
	// Q is the free-text search term applied across name, description, and slug.
	Q string

	// TeamIDs scopes the search to the given teams (enforces tenancy isolation).
	// Every provider implementation is responsible for scoping to these IDs.
	TeamIDs []uuid.UUID

	// Tags optionally narrows results to prompts carrying all listed tags.
	Tags []string

	// Status optionally filters by prompt status ("active", "archived").
	Status *string

	// Vector is a pre-computed query embedding for vector-similarity search.
	// When non-nil, providers that support vector search MUST use it.
	// When nil, providers fall back to keyword/FTS search on Q.
	Vector []float32

	// TopK limits the number of vector results returned (default: 10).
	// Ignored by FTS providers.
	TopK int
}

// SearchResult is a single hit returned by the provider.
type SearchResult struct {
	PromptID uuid.UUID

	// Score is the relevance ranking from the provider.
	// Postgres FTS populates this via ts_rank.
	// External providers (Algolia, ES) return their own native scores.
	// Zero means the provider does not support scoring.
	Score float64

	// Distance is the vector similarity distance from the query embedding.
	// Lower is more similar. Zero means the provider is FTS-only.
	Distance float32
}

// IndexablePrompt is the minimal document shape passed to Index / Deindex.
// Kept separate from model.Prompt to avoid a dependency on the model package
// and to keep this package importable by future external SDK clients.
type IndexablePrompt struct {
	ID          uuid.UUID
	TeamID      uuid.UUID
	Name        string
	Description string
	Slug        string
	Status      string
	Tags        []string

	// Embedding is the pre-computed vector representation of this prompt.
	// Nil means the caller did not supply an embedding (e.g. Postgres FTS path).
	// External vector providers (Qdrant, Pinecone) use this during Index().
	Embedding []float32
}

// Provider is the contract every search backend must implement.
//
// It has two sides:
//   - Query side    → Search()
//   - Mutation side → Index(), Deindex()
//
// For Postgres FTS, Index and Deindex are no-ops because the live table
// columns are the index. For external providers (Algolia, ES, OpenSearch),
// they push/remove documents from the external index.
type Provider interface {
	// Search executes a query and returns an ordered list of matching results.
	// Results are ordered by relevance score descending.
	Search(ctx context.Context, q SearchQuery) ([]SearchResult, error)

	// Index adds or updates a prompt document in the search index.
	// Must be called after a prompt is created or updated.
	Index(ctx context.Context, p IndexablePrompt) error

	// Deindex removes a prompt document from the search index.
	// Must be called after a prompt is deleted or archived.
	Deindex(ctx context.Context, promptID uuid.UUID) error
}
