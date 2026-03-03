// Package freshcache provides core interfaces and types for HTTP response caching.
package freshcache

// Cache is the interface for storing and retrieving cache entries.
// Implementations must be safe for concurrent use by multiple goroutines.
type Cache interface {
	Get(key string) (*Entry, bool)
	Set(key string, entry *Entry)
	Delete(key string)
}
