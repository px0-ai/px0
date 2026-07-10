// Package embedderfactory constructs the active search.Embedder from environment
// configuration. It lives in its own package to avoid import cycles.
package embedderfactory

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/px0-ai/px0/internal/search"
)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]func() search.Embedder)
)

// Register registers a new embedder provider constructor.
// This should be called from an init() function in the provider package.
func Register(name string, constructor func() search.Embedder) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if constructor == nil {
		panic("embedderfactory: Register constructor is nil")
	}
	if _, dup := registry[name]; dup {
		panic("embedderfactory: Register called twice for provider " + name)
	}
	registry[name] = constructor
}

// NewEmbedder reads the EMBEDDER_PROVIDER environment variable and returns
// the appropriate search.Embedder implementation.
func NewEmbedder() (search.Embedder, error) {
	name := os.Getenv("EMBEDDER_PROVIDER")

	if name == "" || name == "noop" {
		log.Println("warn: EMBEDDER_PROVIDER not set, embeddings are disabled")
		return nil, nil // Return nil gracefully, no error.
	}

	registryMu.RLock()
	constructor, ok := registry[name]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown embedder provider %q (did you forget to import it?)", name)
	}

	log.Printf("info: using %s embedding provider", name)
	return constructor(), nil
}
