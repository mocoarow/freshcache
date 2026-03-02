package freshcache_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mocoarow/freshcache"
)

func TestMemoryCache_Get_shouldReturnFalse_whenKeyDoesNotExist(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache()

	// when
	entry, ok := cache.Get("nonexistent")

	// then
	assert.False(t, ok, "ok should be false for nonexistent key")
	assert.Nil(t, entry, "entry should be nil for nonexistent key")
}

func TestMemoryCache_Get_shouldReturnEntry_whenKeyExists(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache()
	entry := &freshcache.Entry{
		StatusCode:   200,
		Header:       http.Header{"Content-Type": {"application/json"}},
		Body:         []byte(`{"id":1}`),
		LastModified: "Thu, 01 Jan 2025 00:00:00 GMT",
		CachedAt:     time.Now(),
	}
	cache.Set("https://api.example.com/users/1", entry)

	// when
	got, ok := cache.Get("https://api.example.com/users/1")

	// then
	require.True(t, ok, "ok should be true for existing key")
	assert.Equal(t, 200, got.StatusCode, "StatusCode should match")
	assert.JSONEq(t, `{"id":1}`, string(got.Body), "Body should match")
	assert.Equal(t, "Thu, 01 Jan 2025 00:00:00 GMT", got.LastModified, "LastModified should match")
}

func TestMemoryCache_Set_shouldOverwriteExistingEntry(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache()
	key := "https://api.example.com/users/1"
	cache.Set(key, &freshcache.Entry{StatusCode: 200, Body: []byte("old")})

	// when
	cache.Set(key, &freshcache.Entry{StatusCode: 200, Body: []byte("new")})

	// then
	got, ok := cache.Get(key)
	require.True(t, ok, "ok should be true after overwrite")
	assert.Equal(t, "new", string(got.Body), "Body should be overwritten value")
}

func TestMemoryCache_Set_shouldBeNoOp_whenEntryIsNil(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache()

	// when / then
	assert.NotPanics(t, func() {
		cache.Set("key", nil)
	}, "setting nil entry should not panic")
	assert.Equal(t, 0, cache.Len(), "cache should be empty after setting nil entry")
}

func TestMemoryCache_Delete_shouldRemoveEntry(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache()
	key := "https://api.example.com/users/1"
	cache.Set(key, &freshcache.Entry{StatusCode: 200})

	// when
	cache.Delete(key)

	// then
	_, ok := cache.Get(key)
	assert.False(t, ok, "ok should be false after delete")
}

func TestMemoryCache_Delete_shouldNotPanic_whenKeyDoesNotExist(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache()

	// when / then
	assert.NotPanics(t, func() {
		cache.Delete("nonexistent")
	}, "deleting nonexistent key should not panic")
}

func TestMemoryCache_Set_shouldEvictLRU_whenMaxEntriesReached(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache(freshcache.WithMaxEntries(2))
	cache.Set("a", &freshcache.Entry{StatusCode: 200, Body: []byte("a")})
	cache.Set("b", &freshcache.Entry{StatusCode: 200, Body: []byte("b")})

	// when - adding third entry should evict "a" (LRU)
	cache.Set("c", &freshcache.Entry{StatusCode: 200, Body: []byte("c")})

	// then
	_, ok := cache.Get("a")
	assert.False(t, ok, "entry 'a' should be evicted as LRU")
	assert.Equal(t, 2, cache.Len(), "cache should have 2 entries")

	got, ok := cache.Get("b")
	require.True(t, ok, "entry 'b' should still exist")
	assert.Equal(t, "b", string(got.Body))

	got, ok = cache.Get("c")
	require.True(t, ok, "entry 'c' should exist")
	assert.Equal(t, "c", string(got.Body))
}

func TestMemoryCache_Get_shouldPromoteToMRU_whenAccessed(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache(freshcache.WithMaxEntries(2))
	cache.Set("a", &freshcache.Entry{StatusCode: 200, Body: []byte("a")})
	cache.Set("b", &freshcache.Entry{StatusCode: 200, Body: []byte("b")})

	// when - access "a" to promote it, then add "c"
	cache.Get("a")
	cache.Set("c", &freshcache.Entry{StatusCode: 200, Body: []byte("c")})

	// then - "b" should be evicted (LRU), "a" should survive
	_, ok := cache.Get("b")
	assert.False(t, ok, "entry 'b' should be evicted as LRU after 'a' was accessed")

	got, ok := cache.Get("a")
	require.True(t, ok, "entry 'a' should still exist (promoted by Get)")
	assert.Equal(t, "a", string(got.Body))
}

func TestMemoryCache_Set_shouldNotEvict_whenMaxEntriesIsZero(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache() // default: no limit

	// when
	for i := range 100 {
		cache.Set(string(rune('a'+i)), &freshcache.Entry{StatusCode: 200})
	}

	// then
	assert.Equal(t, 100, cache.Len(), "cache should hold all entries with no limit")
}

func TestMemoryCache_Get_shouldReturnIndependentCopy(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache()
	cache.Set("key", &freshcache.Entry{
		StatusCode: 200,
		Header:     http.Header{"X-Test": {"original"}},
		Body:       []byte("original"),
	})

	// when - mutate the returned entry
	got, ok := cache.Get("key")
	require.True(t, ok)
	got.StatusCode = 500
	got.Header.Set("X-Test", "mutated")
	got.Body[0] = 'X'

	// then - cache should still have the original
	cached, ok := cache.Get("key")
	require.True(t, ok)
	assert.Equal(t, 200, cached.StatusCode, "StatusCode should not be affected by mutation")
	assert.Equal(t, "original", cached.Header.Get("X-Test"), "Header should not be affected by mutation")
	assert.Equal(t, "original", string(cached.Body), "Body should not be affected by mutation")
}

func TestMemoryCache_ConcurrentAccess_shouldNotPanic(t *testing.T) {
	t.Parallel()

	// given
	cache := freshcache.NewMemoryCache(freshcache.WithMaxEntries(10))
	done := make(chan struct{})

	// when - concurrent reads and writes
	for i := range 10 {
		go func() {
			defer func() { done <- struct{}{} }()
			key := string(rune('a' + i%5))
			for range 100 {
				cache.Set(key, &freshcache.Entry{StatusCode: 200, Body: []byte(key)})
				cache.Get(key)
				cache.Delete(key)
			}
		}()
	}

	// then - all goroutines should complete without panic or race
	for range 10 {
		<-done
	}
}

func TestWithMaxEntries_shouldPanic_whenNegative(t *testing.T) {
	t.Parallel()

	// when / then
	assert.Panics(t, func() {
		freshcache.WithMaxEntries(-1)
	}, "WithMaxEntries(-1) should panic")
}
