package search

import "errors"

var (
	// ErrProviderNotConfigured is returned by the factory (Phase 2) when the
	// SEARCH_PROVIDER env var holds a value with no registered implementation.
	ErrProviderNotConfigured = errors.New("search provider not configured")

	// ErrVectorSearchNotSupported is returned by providers that only support
	// keyword/FTS search when the caller passes a non-nil SearchQuery.Vector.
	ErrVectorSearchNotSupported = errors.New("vector search not supported by this provider")
)
