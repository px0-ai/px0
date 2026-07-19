package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/google/uuid"
	"github.com/px0-ai/px0/internal/model"
)

type ElasticsearchRetriever struct {
	client *elasticsearch.Client
	index  string
}

func NewElasticsearchRetriever() (*ElasticsearchRetriever, error) {
	url := os.Getenv("ELASTICSEARCH_URL")
	if url == "" {
		url = "http://localhost:9200"
	}
	apiKey := os.Getenv("ELASTICSEARCH_API_KEY")
	index := os.Getenv("ELASTICSEARCH_INDEX")
	if index == "" {
		index = "px0_search"
	}

	cfg := elasticsearch.Config{
		Addresses: []string{url},
		APIKey:    apiKey,
	}
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create elasticsearch client: %w", err)
	}

	return &ElasticsearchRetriever{
		client: client,
		index:  index,
	}, nil
}

func (r *ElasticsearchRetriever) Retrieve(ctx context.Context, req Request) ([]Match, error) {
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

	res, err := r.client.Search(
		r.client.Search.WithContext(ctx),
		r.client.Search.WithIndex(r.index),
		r.client.Search.WithBody(strings.NewReader(string(body))),
		r.client.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch search: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch error: %s", res.String())
	}

	var rst struct {
		Hits struct {
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					ID   string `json:"id"`
					Type string `json:"type"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&rst); err != nil {
		return nil, err
	}

	var matches []Match
	for _, hit := range rst.Hits.Hits {
		id, err := uuid.Parse(hit.Source.ID)
		if err == nil {
			matches = append(matches, Match{
				Reference: model.SearchReference{
					Type: model.SearchEntityType(hit.Source.Type),
					ID:   id,
				},
				Score: hit.Score,
			})
		}
	}
	return matches, nil
}
