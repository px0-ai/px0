package search

import (
	"context"
	"fmt"

	"github.com/px0-ai/px0/internal/db"
)

// PostgresRetriever provides the default lexical search using PostgreSQL FTS.
type PostgresRetriever struct{}

func (PostgresRetriever) Retrieve(ctx context.Context, req Request) ([]Match, error) {
	if req.Text == "" || len(req.ProjectIDs) == 0 {
		return []Match{}, nil
	}

	rows, err := db.Pool.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('english', $1) AS value)
		SELECT p.id, ts_rank_cd(p.search_vector, q.value) AS score
		FROM prompts p, q
		WHERE p.project_id = ANY($2)
		  AND p.status = $3
		  AND p.search_vector @@ q.value
		ORDER BY score DESC, p.updated_at DESC, p.id
		LIMIT $4`, req.Text, req.ProjectIDs, req.Status, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("search prompts with postgres: %w", err)
	}
	defer rows.Close()

	matches := make([]Match, 0)
	for rows.Next() {
		var match Match
		if err := rows.Scan(&match.PromptID, &match.Score); err != nil {
			return nil, fmt.Errorf("scan postgres search result: %w", err)
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres search results: %w", err)
	}
	return matches, nil
}
