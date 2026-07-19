package search

import (
	"fmt"
	"os"
	"strings"
)

// NewFromEnv builds the two independent retrieval slots. Provider-specific
// implementations can be added behind these switches without changing the
// handler, public API, reranker, or authorization contract.
func NewFromEnv() (*Engine, error) {
	lexicalName := envOrDefault("SEARCH_FTS_PROVIDER", "postgres")
	semanticName := envOrDefault("SEARCH_VECTOR_PROVIDER", "none")

	lexical, err := lexicalRetriever(lexicalName)
	if err != nil {
		return nil, err
	}
	semantic, err := semanticRetriever(semanticName)
	if err != nil {
		return nil, err
	}
	return NewEngine(lexical, semantic), nil
}

func lexicalRetriever(name string) (Retriever, error) {
	switch name {
	case "postgres":
		return PostgresRetriever{}, nil
	case "elasticsearch":
		return NewElasticsearchRetriever()
	case "opensearch":
		return NewOpenSearchRetriever()
	case "algolia":
		return NewAlgoliaRetriever()
	default:
		return nil, fmt.Errorf("unknown FTS search provider %q", name)
	}
}

func semanticRetriever(name string) (Retriever, error) {
	switch name {
	case "", "none":
		return NoopRetriever{}, nil
	case "qdrant":
		return NewQdrantRetriever()
	case "pinecone":
		return NewPineconeRetriever()
	default:
		return nil, fmt.Errorf("unknown vector search provider %q", name)
	}
}

func envOrDefault(key, fallback string) string {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value
}
