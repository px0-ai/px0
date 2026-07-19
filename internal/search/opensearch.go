package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/px0-ai/px0/internal/model"
)

type OpenSearchRetriever struct {
	client *opensearchapi.Client
	index  string
}

func NewOpenSearchRetriever() (*OpenSearchRetriever, error) {
	url := os.Getenv("OPENSEARCH_URL")
	if url == "" {
		url = "http://localhost:9200"
	}
	username := os.Getenv("OPENSEARCH_USERNAME")
	password := os.Getenv("OPENSEARCH_PASSWORD")
	index := os.Getenv("OPENSEARCH_INDEX")
	if index == "" {
		index = "px0_search"
	}

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{url},
		Username:  username,
		Password:  password,
	})
	if err != nil {
		return nil, fmt.Errorf("create opensearch client: %w", err)
	}

	apiClient := opensearchapi.NewFromClient(client)

	return &OpenSearchRetriever{
		client: apiClient,
		index:  index,
	}, nil
}

func (r *OpenSearchRetriever) Retrieve(ctx context.Context, req Request) ([]Match, error) {
	if req.Text == "" || len(req.ProjectIDs) == 0 {
		return []Match{}, nil
	}

	types := make([]string, len(req.Types))
	for i, t := range req.Types {
		types[i] = string(t)
	}
	projects := make([]string, len(req.ProjectIDs))
	for i, p := range req.ProjectIDs {
		projects[i] = p.String()
	}

	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []map[string]interface{}{
					{"multi_match": map[string]interface{}{
						"query": req.Text,
						"fields": []string{"name^3", "description^2", "slug"},
					}},
				},
				"filter": []map[string]interface{}{
					{"terms": map[string]interface{}{"type": types}},
					{"terms": map[string]interface{}{"project_id": projects}},
				},
			},
		},
		"size": req.Limit,
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	reqOpts := opensearchapi.SearchReq{
		Indices: []string{r.index},
		Body:    strings.NewReader(string(body)),
	}

	res, err := r.client.Search(ctx, &reqOpts)
	if err != nil {
		return nil, fmt.Errorf("opensearch search: %w", err)
	}

	var matches []Match
	for _, hit := range res.Hits.Hits {
        var source struct {
            ID string `json:"id"`
            Type string `json:"type"`
        }
        if err := json.Unmarshal(hit.Source, &source); err == nil {
            id, err := uuid.Parse(source.ID)
            if err == nil {
                matches = append(matches, Match{
                    Reference: model.SearchReference{
                        Type: model.SearchEntityType(source.Type),
                        ID:   id,
                    },
                    Score: float64(hit.Score),
                })
            }
        }
	}
	return matches, nil
}
