// Package searchfactory constructs the active search.Provider from environment
// configuration. It lives in its own package to avoid an import cycle:
//
//	search/postgres → search (interface)
//	searchfactory   → search + search/postgres   (no cycle)
package searchfactory

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/px0-ai/px0/internal/search"
	pgprovider "github.com/px0-ai/px0/internal/search/postgres"
)

// NewProvider reads the SEARCH_PROVIDER environment variable and returns
// the appropriate search.Provider implementation.
//
// Supported values:
//   - "postgres"      → PostgreSQL Full-Text Search
//   - "elasticsearch" → Elasticsearch (not yet implemented)
//   - "opensearch"    → OpenSearch    (not yet implemented)
//   - "algolia"       → Algolia       (not yet implemented)
//   - "qdrant"        → Qdrant Vector Search (not yet implemented)
//   - "pinecone"      → Pinecone Vector Search (not yet implemented)
//   - ""              → defaults to NoopProvider with a warning
//
// If SEARCH_PROVIDER is set to a known-but-unimplemented provider, it returns
// search.ErrProviderNotConfigured so the caller can decide whether to fatal or warn.
func NewProvider(_ context.Context) (search.Provider, error) {
	name := os.Getenv("SEARCH_PROVIDER")

	switch name {
	case "", "noop":
		log.Println("warn: SEARCH_PROVIDER not set, search is disabled (using noop)")
		return search.NoopProvider{}, nil

	case "postgres":
		log.Println("info: using postgres full-text search provider")
		return pgprovider.NewProvider(), nil

	case "elasticsearch", "opensearch", "algolia", "qdrant", "pinecone":
		return nil, fmt.Errorf("%w: %q", search.ErrProviderNotConfigured, name)

	default:
		return nil, fmt.Errorf("%w: unknown provider %q", search.ErrProviderNotConfigured, name)
	}
}
