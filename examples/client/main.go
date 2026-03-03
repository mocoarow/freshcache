// Simple client example demonstrating freshcache-http Transport.
//
// The Transport wraps http.DefaultTransport and caches GET responses
// in memory, so repeated requests to the same URL are served from cache.
//
// Usage:
//
//	go run ./client [url]
//
// If no URL is given, it defaults to http://localhost:8080/time.
package main

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/mocoarow/freshcache"
	freshhttp "github.com/mocoarow/freshcache-http"
)

func main() {
	url := "http://localhost:8080/time"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}

	cache := freshcache.NewMemoryCache()
	transport := freshhttp.NewTransport(
		http.DefaultTransport,
		cache,
		freshhttp.WithTTL(10*time.Second),
	)
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       30 * time.Second,
	}

	for i := range 3 {
		start := time.Now()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			log.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Error("close body", "error", closeErr)
		}
		if err != nil {
			log.Fatal(err)
		}

		elapsed := time.Since(start).Round(time.Millisecond)
		slog.Info("response", "request", i+1, "status", resp.StatusCode, "elapsed", elapsed, "body", string(body))

		if i < 2 {
			time.Sleep(1 * time.Second)
		}
	}
}
