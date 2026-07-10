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

// minFTSRank is the minimum ts_rank score required for a result to be
// included. With OR semantics, a query can match many documents via a
// single common lexeme (e.g. "help" matching "Helps" in a description).
// This threshold filters out coincidental single-term description hits
// while allowing multi-term matches and weighted name matches to surface.
//
// Empirically, with weight A=1.0 (name) and B=0.4 (description):
//   - Single term in description only: ≈0.10-0.12 → filtered
//   - Two+ terms in description:       ≈0.20-0.30 → included
//   - Single term in name (weight A):  ≈0.30+     → included
//
// At 0.15, queries like "quantum physics homework help" that match only
// via a coincidental "help" lexeme are safely excluded, while "app keeps
// crashing" returns prompts with 2+ matching tokens.
const minFTSRank = 0.15

// Search runs a full-text query against the prompts table using OR
// semantics: the query is tokenized by websearch_to_tsquery and then
// AND operators are replaced with OR so a document matches if ANY
// query term is present. Results are ranked by ts_rank and filtered
// to a minimum relevance floor.
//
// When q.Q is empty it returns nil immediately — the caller (handler)
// falls back to the regular store.ListPrompts path.
// Status is applied as a WHERE clause when non-nil (early DB-level filter).
func (p *Provider) Search(ctx context.Context, q search.SearchQuery) ([]search.SearchResult, error) {
	if q.Vector != nil {
		return nil, search.ErrVectorSearchNotSupported
	}
	if q.Q == "" {
		return nil, nil
	}
	if len(q.TeamIDs) == 0 {
		return nil, nil
	}

	args := []any{q.Q, q.TeamIDs}
	whereClauses := []string{
		"team_id = ANY($2)",
		"search_vector @@ q.tsq",
	}

	if q.Status != nil {
		args = append(args, *q.Status)
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", len(args)))
	}

	query := fmt.Sprintf(`
		WITH q AS (
			SELECT replace(websearch_to_tsquery('english', $1)::text, ' & ', ' | ')::tsquery AS tsq
		),
		ranked AS (
			SELECT id, ts_rank(search_vector, q.tsq) AS score
			FROM prompts, q
			WHERE %s
		)
		SELECT id, score
		FROM ranked
		WHERE score >= %f
		ORDER BY score DESC
	`, strings.Join(whereClauses, " AND "), minFTSRank)

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
