package searchfactory_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/px0-ai/px0/internal/search"
	"github.com/px0-ai/px0/internal/searchfactory"
)

// TestNewFTSProvider_Default verifies that an unset SEARCH_FTS_PROVIDER
// resolves to a no-op provider rather than erroring out. This is the
// "out of the box" experience the issue asks for: starting the server
// without any config should not fail.
func TestNewFTSProvider_Default(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "")
	t.Setenv("SEARCH_PROVIDER", "")

	p, err := searchfactory.NewFTSProvider(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if p == nil {
		t.Fatal("expected a non-nil provider")
	}
	// The noop provider reports ErrNotImplemented for any search.
	_, err = p.Search(context.Background(), search.SearchQuery{Q: "anything"})
	if !errors.Is(err, search.ErrNotImplemented) {
		t.Fatalf("expected NoopProvider to return ErrNotImplemented, got %v", err)
	}
}

// TestNewFTSProvider_Postgres verifies that SEARCH_FTS_PROVIDER=postgres
// resolves to a real (non-noop) provider.
func TestNewFTSProvider_Postgres(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "postgres")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "")
	t.Setenv("SEARCH_PROVIDER", "")

	p, err := searchfactory.NewFTSProvider(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// The noop provider would return ErrNotImplemented for any query;
	// the real postgres provider returns nil, nil when the Q is empty
	// (so we can detect the difference even without a database).
	results, err := p.Search(context.Background(), search.SearchQuery{Q: ""})
	if err != nil {
		t.Fatalf("postgres provider should not error on empty Q, got %v", err)
	}
	if results != nil {
		t.Fatalf("postgres provider should return nil results for empty Q, got %v", results)
	}
}

// TestNewFTSProvider_Noop verifies SEARCH_FTS_PROVIDER=noop returns
// a no-op explicitly.
func TestNewFTSProvider_Noop(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "noop")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "")
	t.Setenv("SEARCH_PROVIDER", "")

	p, err := searchfactory.NewFTSProvider(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	_, err = p.Search(context.Background(), search.SearchQuery{Q: "x"})
	if !errors.Is(err, search.ErrNotImplemented) {
		t.Fatalf("expected NoopProvider, got %v", err)
	}
}

// TestNewFTSProvider_UnknownErrors verifies that unknown FTS providers
// surface as ErrProviderNotConfigured.
func TestNewFTSProvider_UnknownErrors(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "")
	t.Setenv("SEARCH_PROVIDER", "wat-is-this")

	_, err := searchfactory.NewFTSProvider(context.Background())
	if err == nil {
		t.Fatal("expected an error for an unknown legacy value")
	}
	if !errors.Is(err, search.ErrProviderNotConfigured) {
		t.Fatalf("expected ErrProviderNotConfigured, got %v", err)
	}
}

// TestNewFTSProvider_UnimplementedStubErrors verifies that known-but-
// unimplemented values return ErrProviderNotConfigured rather than
// silently falling back to noop.
func TestNewFTSProvider_UnimplementedStubErrors(t *testing.T) {
	for _, name := range []string{"elasticsearch", "opensearch", "algolia"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SEARCH_FTS_PROVIDER", name)
			t.Setenv("SEARCH_VECTOR_PROVIDER", "")
			t.Setenv("SEARCH_PROVIDER", "")

			_, err := searchfactory.NewFTSProvider(context.Background())
			if err == nil {
				t.Fatalf("expected an error for stub provider %q", name)
			}
			if !errors.Is(err, search.ErrProviderNotConfigured) {
				t.Fatalf("expected ErrProviderNotConfigured for %q, got %v", name, err)
			}
		})
	}
}

// TestNewVectorProvider_Default verifies the same default-to-noop
// behaviour for the vector slot.
func TestNewVectorProvider_Default(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "")
	t.Setenv("SEARCH_PROVIDER", "")

	p, err := searchfactory.NewVectorProvider(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	_, err = p.Search(context.Background(), search.SearchQuery{Vector: []float32{0.1}})
	if !errors.Is(err, search.ErrNotImplemented) {
		t.Fatalf("expected NoopProvider, got %v", err)
	}
}

// TestNewVectorProvider_PineconeStubErrors verifies that pinecone
// (not yet implemented) returns ErrProviderNotConfigured.
func TestNewVectorProvider_PineconeStubErrors(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "pinecone")
	t.Setenv("SEARCH_PROVIDER", "")

	_, err := searchfactory.NewVectorProvider(context.Background())
	if err == nil {
		t.Fatal("expected an error for pinecone (not yet implemented)")
	}
	if !errors.Is(err, search.ErrProviderNotConfigured) {
		t.Fatalf("expected ErrProviderNotConfigured, got %v", err)
	}
}

// TestNewVectorProvider_UnknownErrors verifies unknown vector values
// surface as ErrProviderNotConfigured.
func TestNewVectorProvider_UnknownErrors(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "wat-is-this")
	t.Setenv("SEARCH_PROVIDER", "")

	_, err := searchfactory.NewVectorProvider(context.Background())
	if err == nil {
		t.Fatal("expected an error for unknown vector provider")
	}
	if !errors.Is(err, search.ErrProviderNotConfigured) {
		t.Fatalf("expected ErrProviderNotConfigured, got %v", err)
	}
}

// TestBackwardCompat_LegacySEARCH_PROVIDER_PostgresRoutesToFTS verifies
// that an existing .env file with only SEARCH_PROVIDER=postgres still
// produces a working FTS provider, exactly as it did before Phase 0.
func TestBackwardCompat_LegacySEARCH_PROVIDER_PostgresRoutesToFTS(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "")
	t.Setenv("SEARCH_PROVIDER", "postgres")

	fts, err := searchfactory.NewFTSProvider(context.Background())
	if err != nil {
		t.Fatalf("FTS provider should resolve via legacy var, got %v", err)
	}
	// Postgres FTS returns (nil, nil) for an empty Q (no FTS query, no error).
	results, err := fts.Search(context.Background(), search.SearchQuery{Q: ""})
	if err != nil {
		t.Fatalf("postgres FTS should not error on empty Q, got %v", err)
	}
	if results != nil {
		t.Fatalf("postgres FTS should return nil for empty Q, got %v", results)
	}

	// Vector side: the legacy "postgres" value should NOT be applied here.
	vec, err := searchfactory.NewVectorProvider(context.Background())
	if err != nil {
		t.Fatalf("vector provider should still default to noop, got %v", err)
	}
	if vec == nil {
		t.Fatal("expected a non-nil vector provider")
	}
	_, err = vec.Search(context.Background(), search.SearchQuery{Vector: []float32{0.1}})
	if !errors.Is(err, search.ErrNotImplemented) {
		t.Fatalf("vector slot should still be noop, got %v", err)
	}
}

// TestBackwardCompat_LegacySEARCH_PROVIDER_QdrantRoutesToVector verifies
// that the legacy SEARCH_PROVIDER=qdrant maps to the vector slot.
func TestBackwardCompat_LegacySEARCH_PROVIDER_QdrantRoutesToVector(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "")
	t.Setenv("SEARCH_PROVIDER", "qdrant")

	// FTS side: qdrant is not FTS, so the FTS slot should fall back to
	// noop rather than masquerading as FTS.
	fts, err := searchfactory.NewFTSProvider(context.Background())
	if err != nil {
		t.Fatalf("FTS provider should not error on qdrant legacy, got %v", err)
	}
	_, err = fts.Search(context.Background(), search.SearchQuery{Q: "x"})
	if !errors.Is(err, search.ErrNotImplemented) {
		t.Fatalf("FTS slot should be noop when legacy=qdrant, got %v", err)
	}

	// Vector side: this WILL try to connect to qdrant. We can't
	// guarantee a running qdrant here, so we only assert the error
	// (if any) is NOT ErrProviderNotConfigured — it should be a
	// connection error or nil, never a "not configured" error.
	_, err = searchfactory.NewVectorProvider(context.Background())
	if err == nil {
		// If it succeeded, fine.
		return
	}
	if errors.Is(err, search.ErrProviderNotConfigured) {
		t.Fatalf("vector provider should resolve via legacy qdrant, got %v", err)
	}
	// Any other error (e.g. connection refused) is acceptable in test
	// environments without a running qdrant.
	if !strings.Contains(err.Error(), "connect") &&
		!strings.Contains(err.Error(), "qdrant") {
		t.Logf("note: legacy qdrant produced error %v (expected if no qdrant is running)", err)
	}
}

// TestBackwardCompat_NewKeysWinOverLegacy verifies that when both the
// new and legacy keys are set, the new key takes precedence.
func TestBackwardCompat_NewKeysWinOverLegacy(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "noop")
	t.Setenv("SEARCH_PROVIDER", "qdrant")

	// New key takes precedence: vector is noop, even though legacy says qdrant.
	p, err := searchfactory.NewVectorProvider(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	_, err = p.Search(context.Background(), search.SearchQuery{Vector: []float32{0.1}})
	if !errors.Is(err, search.ErrNotImplemented) {
		t.Fatalf("new key should win, expected noop, got %v", err)
	}
}
