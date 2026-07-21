package search

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/pinecone-io/go-pinecone/pinecone"
	"github.com/px0-ai/px0/internal/model"
	"google.golang.org/protobuf/types/known/structpb"
)

type PineconeRetriever struct {
	client *pinecone.Client
	index  string
}

func NewPineconeRetriever() (*PineconeRetriever, error) {
	apiKey := os.Getenv("PINECONE_API_KEY")
	index := os.Getenv("PINECONE_INDEX")
	if index == "" {
		index = "px0-search"
	}

	client, err := pinecone.NewClient(pinecone.NewClientParams{
		ApiKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create pinecone client: %w", err)
	}

	return &PineconeRetriever{
		client: client,
		index:  index,
	}, nil
}

func (r *PineconeRetriever) Retrieve(ctx context.Context, req Request) ([]Match, error) {
	if req.Text == "" || len(req.ProjectIDs) == 0 {
		return []Match{}, nil
	}
    
    idx, err := r.client.Index(pinecone.NewIndexConnParams{
        Host: r.index, 
    })
    if err != nil {
        return nil, err
    }

	types := make([]interface{}, len(req.Types))
	for i, t := range req.Types {
		types[i] = string(t)
	}
	projects := make([]interface{}, len(req.ProjectIDs))
	for i, p := range req.ProjectIDs {
		projects[i] = p.String()
	}

    filter := map[string]interface{}{
        "type": map[string]interface{}{"$in": types},
        "project_id": map[string]interface{}{"$in": projects},
    }

	filterStruct, err := structpb.NewStruct(filter)
	if err != nil {
		return nil, err
	}

    dummyVector := make([]float32, 1536) 
    
	res, err := idx.QueryByVectorValues(ctx, &pinecone.QueryByVectorValuesRequest{
		Vector: dummyVector,
		TopK:   uint32(req.Limit),
		MetadataFilter: filterStruct,
        IncludeMetadata: true,
	})
	if err != nil {
		return nil, fmt.Errorf("pinecone search: %w", err)
	}

	var matches []Match
	for _, match := range res.Matches {
		if match.Vector == nil {
			continue
		}
        meta := match.Vector.Metadata
        var typeStr, idStr string
        if meta != nil && meta.AsMap() != nil {
            if t, ok := meta.AsMap()["type"].(string); ok {
                typeStr = t
            }
            if id, ok := meta.AsMap()["id"].(string); ok {
                idStr = id
            }
        }
        
        if idStr == "" {
            idStr = match.Vector.Id
        }

		id, err := uuid.Parse(idStr)
		if err == nil {
			matches = append(matches, Match{
				Reference: model.SearchReference{
					Type: model.SearchEntityType(typeStr),
					ID:   id,
				},
				Score: float64(match.Score),
			})
		}
	}

	return matches, nil
}
