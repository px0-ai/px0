package search

import (
	"sync/atomic"
)

var defaultProvider atomic.Pointer[Provider]

func init() {
	// Start safely with a NoopProvider so we never panic on a nil dereference
	// before main.go finishes initialization.
	var p Provider = NoopProvider{}
	defaultProvider.Store(&p)
}

// Get returns the active search provider for the process.
// It is lock-free, safe for concurrent reads, and guaranteed to never return nil.
func Get() Provider {
	return *defaultProvider.Load()
}

// Init initialises the package-level default provider using the given Provider.
// The provider is constructed by searchfactory.NewProvider() in main.go, which
// keeps the import graph cycle-free (search/postgres → search, searchfactory → both).
func Init(p Provider) {
	defaultProvider.Store(&p)
}
