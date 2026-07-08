package search

import (
	"context"

	"github.com/google/uuid"
)

// Compile-time assertion: NoopProvider must implement Provider.
// If the interface changes and NoopProvider drifts, this line will not compile.
var _ Provider = NoopProvider{}

// NoopProvider satisfies Provider with silent no-ops.
// Use it in tests or as a stand-in while implementing a real provider.
type NoopProvider struct{}

func (NoopProvider) Search(_ context.Context, q SearchQuery) ([]SearchResult, error) {
	return nil, ErrNotImplemented
}

func (NoopProvider) Index(_ context.Context, _ IndexablePrompt) error {
	return nil
}

func (NoopProvider) Deindex(_ context.Context, _ uuid.UUID) error {
	return nil
}
