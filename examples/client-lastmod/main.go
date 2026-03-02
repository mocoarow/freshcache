// Client example for the server-lastmod server.
//
// Demonstrates how the client-side cache (freshcache-http Transport) works
// together with the server-side LastModifiedFunc. The Transport sends
// If-Modified-Since automatically and handles 304 responses.
//
// Usage:
//
//	# Terminal 1: start the server
//	go run ./server-lastmod
//
//	# Terminal 2: run this client
//	go run ./client-lastmod
//
// Expected output:
//
//	request 1: status=200 (handler called, response cached on both sides)
//	request 2: status=200 (client TTL expired → sends IMS → server returns 304 or cache)
//	request 3: status=200 (after PUT, data changed → fresh response)
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/mocoarow/freshcache"
	freshhttp "github.com/mocoarow/freshcache-http"
)

func main() {
	cache := freshcache.NewMemoryCache()
	transport := freshhttp.NewTransport(
		http.DefaultTransport,
		cache,
		freshhttp.WithTTL(1*time.Second),
	)
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       30 * time.Second,
	}

	baseURL := "http://localhost:8080"

	// 1st GET — cache miss, handler called
	if err := get(client, baseURL+"/item", 1); err != nil {
		log.Fatal(err)
	}

	time.Sleep(2 * time.Second)

	// 2nd GET — client TTL expired, but server data unchanged
	if err := get(client, baseURL+"/item", 2); err != nil {
		log.Fatal(err)
	}

	// PUT — update the data on server
	slog.Info("PUT /item (update data)")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, baseURL+"/item", nil)
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
	slog.Info("PUT response", "status", resp.StatusCode, "body", string(body))

	time.Sleep(2 * time.Second)

	// 3rd GET — data changed, fresh response
	if err := get(client, baseURL+"/item", 3); err != nil {
		log.Fatal(err)
	}
}

func get(client *http.Client, url string, n int) error {
	start := time.Now()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		slog.Error("close body", "error", closeErr)
	}
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	slog.Info("response", "request", n, "status", resp.StatusCode, "elapsed", elapsed, "body", string(body))
	return nil
}
