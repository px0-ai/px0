package search

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Compile-time check: NoopProvider must satisfy Provider interface
var _ Provider = (*NoopProvider)(nil)

func TestNoopProvider_Search_TextQuery(t *testing.T) {
	p := &NoopProvider{}
	results, err := p.Search(context.Background(), SearchQuery{Q: "test"})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestNoopProvider_Search_VectorQuery(t *testing.T) {
	p := &NoopProvider{}
	results, err := p.Search(context.Background(), SearchQuery{Vector: []float32{1.0, 2.0}})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestNoopProvider_Index(t *testing.T) {
	p := &NoopProvider{}
	err := p.Index(context.Background(), IndexablePrompt{ID: uuid.New()})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNoopProvider_Deindex(t *testing.T) {
	p := &NoopProvider{}
	err := p.Deindex(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
