package search

import (
	"context"
	"fmt"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

// PostgresRetriever provides the default lexical search using PostgreSQL FTS.
type PostgresRetriever struct{}

func (PostgresRetriever) Retrieve(ctx context.Context, req Request) ([]Match, error) {
	if req.Text == "" || len(req.ProjectIDs) == 0 {
		return []Match{}, nil
	}

	types := make([]string, len(req.Types))
	for i, entityType := range req.Types {
		types[i] = string(entityType)
	}

	rows, err := db.Pool.Query(ctx, `
		WITH q AS (SELECT websearch_to_tsquery('english', $1) AS value),
		matches AS (
			SELECT 'prompt'::text AS entity_type, p.id,
			       ts_rank_cd(p.search_vector, q.value) AS score, p.updated_at
			FROM prompts p, q
			WHERE 'prompt' = ANY($3) AND p.project_id = ANY($2) AND p.status = 'active'
			  AND p.search_vector @@ q.value
			UNION ALL
			SELECT 'skill'::text, s.id,
			       ts_rank_cd(s.search_vector, q.value), s.updated_at
			FROM skills s, q
			WHERE 'skill' = ANY($3) AND s.project_id = ANY($2) AND s.search_vector @@ q.value
			UNION ALL
			SELECT 'tool'::text, t.id,
			       ts_rank_cd(t.search_vector, q.value), t.updated_at
			FROM tools t, q
			WHERE 'tool' = ANY($3) AND t.project_id = ANY($2) AND t.search_vector @@ q.value
		)
		SELECT entity_type, id, score
		FROM matches
		WHERE entity_type = ANY($3)
		ORDER BY score DESC, updated_at DESC, entity_type, id
		LIMIT $4`, req.Text, req.ProjectIDs, types, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("search registry entities with postgres: %w", err)
	}
	defer rows.Close()

	matches := make([]Match, 0)
	for rows.Next() {
		var match Match
		var entityType string
		if err := rows.Scan(&entityType, &match.Reference.ID, &match.Score); err != nil {
			return nil, fmt.Errorf("scan postgres search result: %w", err)
		}
		match.Reference.Type = model.SearchEntityType(entityType)
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate postgres search results: %w", err)
	}
	return matches, nil
}
