package search

import "context"

// NoopRetriever represents an intentionally disabled search slot.
type NoopRetriever struct{}

func (NoopRetriever) Retrieve(context.Context, Request) ([]Match, error) {
	return []Match{}, nil
}
