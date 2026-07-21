package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

// GetSearchResults hydrates ranked references through an authorization-scoped
// query. Reapplying project scope here prevents a remote or faulty retriever
// from returning entities the requester cannot access.
func GetSearchResults(ctx context.Context, references []model.SearchReference, projectIDs []uuid.UUID) ([]*model.SearchResult, error) {
	if len(references) == 0 || len(projectIDs) == 0 {
		return []*model.SearchResult{}, nil
	}

	idsByType := map[model.SearchEntityType][]uuid.UUID{
		model.SearchEntityPrompt: {},
		model.SearchEntitySkill:  {},
		model.SearchEntityTool:   {},
	}
	for _, reference := range references {
		idsByType[reference.Type] = append(idsByType[reference.Type], reference.ID)
	}

	rows, err := db.Pool.Query(ctx, `
		SELECT 'prompt'::text, id, project_id, slug, name, description, created_at, updated_at
		FROM prompts
		WHERE id = ANY($1) AND project_id = ANY($4) AND status = 'active'
		UNION ALL
		SELECT 'skill'::text, id, project_id, slug, name, description, created_at, updated_at
		FROM skills
		WHERE id = ANY($2) AND project_id = ANY($4)
		UNION ALL
		SELECT 'tool'::text, id, project_id, slug, name, description, created_at, updated_at
		FROM tools
		WHERE id = ANY($3) AND project_id = ANY($4)`,
		idsByType[model.SearchEntityPrompt],
		idsByType[model.SearchEntitySkill],
		idsByType[model.SearchEntityTool],
		projectIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("get search results: %w", err)
	}
	defer rows.Close()

	byReference := make(map[model.SearchReference]*model.SearchResult, len(references))
	for rows.Next() {
		result := &model.SearchResult{}
		var entityType string
		if err := rows.Scan(
			&entityType,
			&result.ID,
			&result.ProjectID,
			&result.Slug,
			&result.Name,
			&result.Description,
			&result.CreatedAt,
			&result.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		result.Type = model.SearchEntityType(entityType)
		byReference[model.SearchReference{Type: result.Type, ID: result.ID}] = result
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}

	results := make([]*model.SearchResult, 0, len(byReference))
	for _, reference := range references {
		if result, ok := byReference[reference]; ok {
			results = append(results, result)
		}
	}
	return results, nil
}
