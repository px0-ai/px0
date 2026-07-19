package search

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	qdrant "github.com/qdrant/go-client/qdrant"
	"github.com/px0-ai/px0/internal/model"
)

type QdrantRetriever struct {
	client *qdrant.Client
	collection string
}

func NewQdrantRetriever() (*QdrantRetriever, error) {
	url := os.Getenv("QDRANT_URL")
	if url == "" {
		url = "localhost" 
	}
    apiKey := os.Getenv("QDRANT_API_KEY")
	collection := os.Getenv("QDRANT_COLLECTION")
	if collection == "" {
		collection = "px0_search"
	}

    client, err := qdrant.NewClient(&qdrant.Config{
        Host: url,
        Port: 6334,
        APIKey: apiKey,
		SkipCompatibilityCheck: true,
    })
    
	if err != nil {
		return nil, fmt.Errorf("create qdrant client: %w", err)
	}

	return &QdrantRetriever{
		client: client,
		collection: collection,
	}, nil
}

func (r *QdrantRetriever) Retrieve(ctx context.Context, req Request) ([]Match, error) {
	if req.Text == "" || len(req.ProjectIDs) == 0 {
		return []Match{}, nil
	}

    vector := make([]float32, 1536)

    var typeConditions []*qdrant.Condition
    for _, t := range req.Types {
        typeConditions = append(typeConditions, qdrant.NewMatch("type", string(t)))
    }
    
    var projectConditions []*qdrant.Condition
    for _, p := range req.ProjectIDs {
        projectConditions = append(projectConditions, qdrant.NewMatch("project_id", p.String()))
    }

    filter := &qdrant.Filter{
        Must: []*qdrant.Condition{
            qdrant.NewFilterAsCondition(&qdrant.Filter{
                Should: typeConditions,
            }),
            qdrant.NewFilterAsCondition(&qdrant.Filter{
                Should: projectConditions,
            }),
        },
    }

	res, err := r.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: r.collection,
		Query:          qdrant.NewQuery(vector...),
		Filter:         filter,
		Limit:          func(l uint64) *uint64 { return &l }(uint64(req.Limit)),
        WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}

	var matches []Match
	for _, p := range res {
        payload := p.GetPayload()
        typeVal := payload["type"].GetStringValue()
        idVal := payload["id"].GetStringValue()

		if idVal == "" && p.GetId() != nil {
			idVal = p.GetId().GetUuid()
		}

		id, err := uuid.Parse(idVal)
		if err == nil {
			matches = append(matches, Match{
				Reference: model.SearchReference{
					Type: model.SearchEntityType(typeVal),
					ID:   id,
				},
				Score: float64(p.GetScore()),
			})
		}
	}

	return matches, nil
}
