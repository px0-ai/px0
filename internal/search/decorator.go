package search

import "context"

// embeddingDecorator wraps a Provider. It exists to give the vector
// provider a place to auto-embed prompts on the Index() mutation path
// (so callers don't have to compute embeddings themselves).
//
// Search() is a pure pass-through: it does NOT auto-embed text queries
// into vectors. Auto-embedding on the search path was a bug — it made
// mode=fts silently do vector search when an embedder was registered
// and gave callers no way to ask for FTS-only behaviour. Embedding for
// search is now the caller's responsibility (see
// internal/handler/prompt.go:resolveVectorFromQuery, which is the
// only place a text query is converted to a vector for search).
type embeddingDecorator struct {
	Provider
}

// Search delegates to the underlying Provider with no embedding side
// effect. The caller's SearchQuery.Vector is passed through unchanged.
func (d embeddingDecorator) Search(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	return d.Provider.Search(ctx, q)
}

// Index intercepts the prompt, generates an embedding if an active
// Embedder exists, and delegates to the underlying Provider. This is
// the only place auto-embed is allowed to happen, and it only happens
// on the mutation path (writing a prompt to the vector store), not on
// the query path.
func (d embeddingDecorator) Index(ctx context.Context, p IndexablePrompt) error {
	embedder := GetEmbedder()
	if len(p.Embedding) == 0 && embedder != nil {
		// We embed the name and description to capture the semantics of the prompt
		vector, err := embedder.Embed(ctx, p.Name+" "+p.Description)
		if err != nil {
			return err
		}
		p.Embedding = vector
	}
	return d.Provider.Index(ctx, p)
}
