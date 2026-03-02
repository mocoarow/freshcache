// Server example demonstrating WithLastModified option.
//
// This server manages an in-memory item whose updated_at timestamp
// is tracked separately from the cache. When the cache TTL expires,
// the middleware calls LastModifiedFunc to check whether the data has
// actually changed, avoiding unnecessary handler execution.
//
// Usage:
//
//	go run ./server-lastmod
//
// Then test with:
//
//	curl -i http://localhost:8080/item          # handler called, response cached
//	curl -i http://localhost:8080/item          # TTL expired but data unchanged → cached response
//	curl -X PUT http://localhost:8080/item      # update data (bumps updated_at)
//	curl -i http://localhost:8080/item          # data changed → handler called again
//	curl -H "If-Modified-Since: Sat, 01 Jan 2028 00:00:00 GMT" http://localhost:8080/item  # 304
package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mocoarow/freshcache"
	freshserver "github.com/mocoarow/freshcache-server"
)

type item struct {
	mu        sync.RWMutex
	value     string
	updatedAt time.Time
}

func (it *item) get() (string, time.Time) {
	it.mu.RLock()
	defer it.mu.RUnlock()
	return it.value, it.updatedAt
}

func (it *item) update(value string) time.Time {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.value = value
	it.updatedAt = time.Now().UTC().Truncate(time.Second)
	return it.updatedAt
}

var store = &item{
	value:     "hello",
	updatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
}

func main() {
	cache := freshcache.NewMemoryCache()
	mw := freshserver.NewMiddleware(cache, 1*time.Second,
		freshserver.WithLastModified(func(_ *http.Request) (time.Time, error) {
			_, t := store.get()
			return t, nil
		}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /item", handleGetItem)
	mux.HandleFunc("PUT /item", handleUpdateItem)

	handler := mw.Wrap(mux)

	addr := ":8080"
	slog.Info("listening", "addr", addr, "ttl", "1s", "lastModifiedFunc", true)

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func handleGetItem(w http.ResponseWriter, _ *http.Request) {
	slog.Info("handler called")

	value, updatedAt := store.get()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Last-Modified", updatedAt.Format(http.TimeFormat))
	if err := json.NewEncoder(w).Encode(map[string]string{
		"value":      value,
		"updated_at": updatedAt.Format(time.RFC3339),
	}); err != nil {
		slog.Error("encode response", "error", err)
	}
}

func handleUpdateItem(w http.ResponseWriter, _ *http.Request) {
	t := store.update("updated at " + time.Now().Format(time.RFC3339))
	slog.Info("data updated", "updated_at", t.Format(time.RFC3339))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"updated_at": t.Format(time.RFC3339),
	}); err != nil {
		slog.Error("encode response", "error", err)
	}
}
