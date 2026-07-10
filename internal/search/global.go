package search

import (
	"sync/atomic"
)

var (
	defaultFTSProvider     atomic.Pointer[Provider]
	defaultVectorProvider   atomic.Pointer[Provider]
)

func init() {
	// Start safely with NoopProviders so we never panic on a nil dereference
	// before main.go finishes initialization.
	var p Provider = NoopProvider{}
	defaultFTSProvider.Store(&p)
	defaultVectorProvider.Store(&p)
}

// GetFTS returns the active FTS search provider for the process.
// It is lock-free, safe for concurrent reads, and guaranteed to never return nil.
func GetFTS() Provider {
	return *defaultFTSProvider.Load()
}

// GetVector returns the active vector search provider for the process.
// It is lock-free, safe for concurrent reads, and guaranteed to never return nil.
func GetVector() Provider {
	return *defaultVectorProvider.Load()
}

// Init initialises the package-level FTS and vector providers.
//
// The two slots are independent: the FTS provider (e.g. postgres FTS) is
// installed unwrapped because its Index is already a no-op and auto-embed
// on the search path is the bug this refactor fixes. The vector provider
// is wrapped in embeddingDecorator so that the handler can still rely on
// Index() uploading an embedding when one is available — but Search()
// does NOT auto-embed (see decorator.go).
func Init(fts Provider, vector Provider) {
	// FTS is installed as-is. Wrapping it with the embedding decorator
	// has no effect on Search (which we just made a pass-through) and
	// would only add confusion, since FTS providers don't consume vectors.
	defaultFTSProvider.Store(&fts)
	var wrappedVector Provider = embeddingDecorator{Provider: vector}
	defaultVectorProvider.Store(&wrappedVector)
}
