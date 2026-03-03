// Simple server example demonstrating freshcache-server middleware.
//
// The middleware caches GET responses in memory so repeated requests
// are served from cache without hitting the handler.
//
// Usage:
//
//	go run ./server
//
// Then test with:
//
//	curl -i http://localhost:8080/time
//	curl -i http://localhost:8080/time   # same response (cached)
package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/mocoarow/freshcache"
	freshserver "github.com/mocoarow/freshcache-server"
)

func main() {
	cache := freshcache.NewMemoryCache()
	mw := freshserver.NewMiddleware(cache, 5*time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /time", handleTime)

	handler := mw.Wrap(mux)

	addr := ":8080"
	slog.Info("listening", "addr", addr, "ttl", "5s")

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func handleTime(w http.ResponseWriter, _ *http.Request) {
	slog.Info("handler called")

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"now": time.Now().Format(time.RFC3339Nano),
	}); err != nil {
		slog.Error("encode response", "error", err)
	}
}
