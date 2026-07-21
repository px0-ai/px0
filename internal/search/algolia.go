package search

import (
	"context"
	"fmt"
	"os"

	"github.com/algolia/algoliasearch-client-go/v4/algolia/search"
	"github.com/google/uuid"
	"github.com/px0-ai/px0/internal/model"
)

type AlgoliaRetriever struct {
	client *search.APIClient
	index  string
}

func NewAlgoliaRetriever() (*AlgoliaRetriever, error) {
	appID := os.Getenv("ALGOLIA_APP_ID")
	apiKey := os.Getenv("ALGOLIA_API_KEY")
	index := os.Getenv("ALGOLIA_INDEX_NAME")
	if index == "" {
		index = "px0_search"
	}

	client, err := search.NewClient(appID, apiKey)
	if err != nil {
	    return nil, err
	}

	return &AlgoliaRetriever{
		client: client,
		index:  index,
	}, nil
}

func (r *AlgoliaRetriever) Retrieve(ctx context.Context, req Request) ([]Match, error) {
	if req.Text == "" || len(req.ProjectIDs) == 0 {
		return []Match{}, nil
	}

	var typeFilters []string
	for _, t := range req.Types {
		typeFilters = append(typeFilters, fmt.Sprintf("type:%s", t))
	}
	
	var projectFilters []string
	for _, p := range req.ProjectIDs {
		projectFilters = append(projectFilters, fmt.Sprintf("project_id:%s", p.String()))
	}

    var filterStrings []string
    if len(typeFilters) > 0 {
        var combined string
        for i, tf := range typeFilters {
            if i > 0 {
                combined += " OR "
            }
            combined += tf
        }
        filterStrings = append(filterStrings, "(" + combined + ")")
    }

    if len(projectFilters) > 0 {
        var combined string
        for i, pf := range projectFilters {
            if i > 0 {
                combined += " OR "
            }
            combined += pf
        }
        filterStrings = append(filterStrings, "(" + combined + ")")
    }
    
    var finalFilter string
    for i, f := range filterStrings {
        if i > 0 {
            finalFilter += " AND "
        }
        finalFilter += f
    }
    
	searchParams := search.NewSearchParamsObject().
		SetQuery(req.Text).
		SetFilters(finalFilter).
		SetHitsPerPage(int32(req.Limit))

	res, err := r.client.SearchSingleIndex(r.client.NewApiSearchSingleIndexRequest(r.index).WithSearchParams(search.SearchParamsObjectAsSearchParams(searchParams)))
	if err != nil {
		return nil, fmt.Errorf("algolia search: %w", err)
	}

	var matches []Match
	for _, hit := range res.Hits {
		typeVal, ok := hit.AdditionalProperties["type"].(string)
		if !ok {
			continue
		}
		
		idStr := hit.ObjectID
		
		id, err := uuid.Parse(idStr)
		if err == nil {
			matches = append(matches, Match{
				Reference: model.SearchReference{
					Type: model.SearchEntityType(typeVal),
					ID:   id,
				},
				Score: 1.0, 
			})
		}
	}

	return matches, nil
}
