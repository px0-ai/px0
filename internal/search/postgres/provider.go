package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/search"
)

// Compile-time assertion: Provider must implement search.Provider.
var _ search.Provider = &Provider{}

// Provider implements search.Provider using PostgreSQL Full-Text Search.
// It uses a pre-computed tsvector column (search_vector) maintained by a
// database trigger, queried via websearch_to_tsquery for natural-language input.
type Provider struct{}

// NewProvider returns a Postgres-backed search.Provider.
// Requires db.Pool to already be initialised (called after db.Connect in main.go).
func NewProvider() search.Provider {
	return &Provider{}
}

// Search runs a full-text query against the prompts table.
// When q.Q is empty it returns nil immediately — the caller (handler) falls
// back to the regular store.ListPrompts path.
// Status is applied as a WHERE clause when non-nil (early DB-level filter).
func (p *Provider) Search(ctx context.Context, q search.SearchQuery) ([]search.SearchResult, error) {
	if q.Q == "" {
		return nil, nil
	}
	if len(q.TeamIDs) == 0 {
		return nil, nil
	}

	args := []any{q.Q, q.TeamIDs}
	clauses := []string{
		"team_id = ANY($2)",
		"search_vector @@ websearch_to_tsquery('english', $1)",
	}

	// Optional status filter — reduces result set before ID hydration.
	if q.Status != nil {
		args = append(args, *q.Status)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}

	query := fmt.Sprintf(`
		SELECT id, ts_rank(search_vector, websearch_to_tsquery('english', $1)) AS score
		FROM prompts
		WHERE %s
		ORDER BY score DESC
	`, strings.Join(clauses, " AND "))

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres search: %w", err)
	}
	defer rows.Close()

	var results []search.SearchResult
	for rows.Next() {
		var r search.SearchResult
		if err := rows.Scan(&r.PromptID, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Index is a no-op for Postgres FTS — the search_vector column is kept
// up to date automatically by the database trigger on every INSERT/UPDATE.
func (p *Provider) Index(_ context.Context, _ search.IndexablePrompt) error {
	return nil
}

// Deindex is a no-op for Postgres FTS — cascade deletes on the prompts
// table remove the row and its tsvector automatically.
func (p *Provider) Deindex(_ context.Context, _ uuid.UUID) error {
	return nil
}
