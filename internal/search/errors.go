package search

import "errors"

var (
	// ErrProviderNotConfigured is returned by the factory (Phase 2) when the
	// SEARCH_PROVIDER env var holds a value with no registered implementation.
	ErrProviderNotConfigured = errors.New("search provider not configured")
)
