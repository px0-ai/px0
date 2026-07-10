package search

import "context"

// Embedder generates vector embeddings for text.
// This interface allows us to easily swap out embedding models (HuggingFace, OpenAI, local)
// without changing the application logic.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

var activeEmbedder Embedder

// SetEmbedder configures the global embedding provider.
func SetEmbedder(e Embedder) {
	activeEmbedder = e
}

// GetEmbedder returns the active embedding provider.
func GetEmbedder() Embedder {
	return activeEmbedder
}
