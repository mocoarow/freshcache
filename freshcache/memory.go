package freshcache

import (
	"container/list"
	"sync"
)

// MemoryCache is a thread-safe in-memory Cache implementation with optional LRU eviction.
type MemoryCache struct {
	mu         sync.RWMutex
	entries    map[string]*list.Element
	order      *list.List
	maxEntries int
}

type cacheItem struct {
	key   string
	entry *Entry
}

// MemoryCacheOption configures a MemoryCache.
type MemoryCacheOption func(*MemoryCache)

// WithMaxEntries sets the maximum number of cache entries.
// When the limit is reached, the least recently used entry is evicted.
// A value of 0 means no limit (default). Negative values will panic.
func WithMaxEntries(n int) MemoryCacheOption {
	if n < 0 {
		panic("freshcache: maxEntries must not be negative")
	}
	return func(c *MemoryCache) { c.maxEntries = n }
}

// NewMemoryCache creates a new MemoryCache.
func NewMemoryCache(opts ...MemoryCacheOption) *MemoryCache {
	c := &MemoryCache{
		mu:         sync.RWMutex{},
		entries:    make(map[string]*list.Element),
		order:      list.New(),
		maxEntries: 0,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get returns a deep copy of the cached entry for the given key.
// The returned entry is safe to mutate without affecting the cache.
// Lock (not RLock) is required because MoveToFront mutates the LRU list.
func (c *MemoryCache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(elem)
	return itemOf(elem).entry.clone(), true
}

// Set stores a deep copy of the given entry in the cache under the given key.
// The caller may safely mutate the entry after calling Set.
// If entry is nil, Set is a no-op.
func (c *MemoryCache) Set(key string, entry *Entry) {
	if entry == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.order.MoveToFront(elem)
		itemOf(elem).entry = entry.clone()
		return
	}
	if c.maxEntries > 0 && c.order.Len() >= c.maxEntries {
		c.removeLRU()
	}
	item := &cacheItem{key: key, entry: entry.clone()}
	elem := c.order.PushFront(item)
	c.entries[key] = elem
}

// Delete removes the entry for the given key from the cache.
func (c *MemoryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.removeElement(elem)
	}
}

// Len returns the number of entries in the cache.
func (c *MemoryCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.order.Len()
}

func (c *MemoryCache) removeLRU() {
	back := c.order.Back()
	if back != nil {
		c.removeElement(back)
	}
}

func (c *MemoryCache) removeElement(elem *list.Element) {
	c.order.Remove(elem)
	delete(c.entries, itemOf(elem).key)
}

func itemOf(elem *list.Element) *cacheItem {
	item, _ := elem.Value.(*cacheItem)
	return item
}
