// Package freshhttp provides a caching http.RoundTripper for HTTP clients.
package freshhttp

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mocoarow/freshcache"
)

const defaultMaxBodySize int64 = 10 * 1024 * 1024 // 10 MB

// Transport is an http.RoundTripper that caches GET responses.
type Transport struct {
	base        http.RoundTripper
	cache       freshcache.Cache
	ttl         time.Duration
	cacheErrors bool
	maxBodySize int64
}

// Option configures a Transport.
type Option func(*Transport)

// WithTTL sets the cache TTL.
func WithTTL(ttl time.Duration) Option {
	return func(t *Transport) {
		t.ttl = ttl
	}
}

// WithCacheErrors controls whether error responses (4xx/5xx) are cached.
func WithCacheErrors(v bool) Option {
	return func(t *Transport) {
		t.cacheErrors = v
	}
}

// WithMaxBodySize sets the maximum response body size that will be cached.
// Responses larger than this are passed through without caching.
// Default is 10 MB. A value of 0 means no limit. Negative values will panic.
func WithMaxBodySize(n int64) Option {
	if n < 0 {
		panic("freshhttp: maxBodySize must not be negative")
	}
	return func(t *Transport) {
		t.maxBodySize = n
	}
}

// NewTransport creates a new caching Transport wrapping the given base RoundTripper.
// If base is nil, http.DefaultTransport is used.
// cache must not be nil; passing nil will panic.
func NewTransport(base http.RoundTripper, cache freshcache.Cache, opts ...Option) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	if cache == nil {
		panic("freshhttp: cache must not be nil")
	}
	t := &Transport{
		base:        base,
		cache:       cache,
		ttl:         0,
		cacheErrors: false,
		maxBodySize: defaultMaxBodySize,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.forwardRequest(req)
	}

	key := req.URL.String()

	entry, ok := t.cache.Get(key)
	if ok && (t.ttl == 0 || time.Since(entry.CachedAt) < t.ttl) {
		return t.responseFromEntry(entry, req), nil
	}

	return t.fetchAndCache(req, key, entry, ok)
}

func (t *Transport) forwardRequest(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("forward request: %w", err)
	}
	return resp, nil
}

func (t *Transport) fetchAndCache(req *http.Request, key string, entry *freshcache.Entry, hasEntry bool) (*http.Response, error) {
	if hasEntry && entry.LastModified != "" {
		req = req.Clone(req.Context())
		req.Header.Set("If-Modified-Since", entry.LastModified)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("round trip: %w", err)
	}

	if resp.StatusCode == http.StatusNotModified && hasEntry {
		return t.handleNotModified(resp, req, key, entry)
	}

	return t.readAndCache(resp, req, key)
}

func (t *Transport) handleNotModified(resp *http.Response, req *http.Request, key string, entry *freshcache.Entry) (*http.Response, error) {
	refreshed := &freshcache.Entry{
		StatusCode:   entry.StatusCode,
		Header:       entry.Header,
		Body:         entry.Body,
		LastModified: entry.LastModified,
		CachedAt:     time.Now(),
	}
	t.cache.Set(key, refreshed)
	if err := resp.Body.Close(); err != nil {
		return nil, fmt.Errorf("close response body: %w", err)
	}
	return t.responseFromEntry(refreshed, req), nil
}

func (t *Transport) readAndCache(resp *http.Response, req *http.Request, key string) (*http.Response, error) {
	body, err := t.readBody(resp, req)
	if err != nil {
		return nil, err
	}

	if t.maxBodySize > 0 && int64(len(body)) > t.maxBodySize {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{
			Reader: io.MultiReader(bytes.NewReader(body), resp.Body),
			Closer: resp.Body,
		}
		return resp, nil
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		return nil, fmt.Errorf("close response body: %w", closeErr)
	}

	if resp.StatusCode >= 400 && !t.cacheErrors {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}

	newEntry := &freshcache.Entry{
		StatusCode:   resp.StatusCode,
		Header:       resp.Header.Clone(),
		Body:         body,
		LastModified: resp.Header.Get("Last-Modified"),
		CachedAt:     time.Now(),
	}
	t.cache.Set(key, newEntry)

	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func (t *Transport) readBody(resp *http.Response, req *http.Request) ([]byte, error) {
	var reader io.Reader = resp.Body
	if t.maxBodySize > 0 {
		reader = io.LimitReader(resp.Body, t.maxBodySize+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("close response body after read error", //nolint:gosec // G706: structured slog, not format string
				"url", req.URL.String(), "error", closeErr)
		}
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return body, nil
}

func (t *Transport) responseFromEntry(entry *freshcache.Entry, req *http.Request) *http.Response {
	return &http.Response{
		Status:           fmt.Sprintf("%d %s", entry.StatusCode, http.StatusText(entry.StatusCode)),
		StatusCode:       entry.StatusCode,
		Proto:            "HTTP/1.1",
		ProtoMajor:       1,
		ProtoMinor:       1,
		Header:           entry.Header.Clone(),
		Body:             io.NopCloser(bytes.NewReader(entry.Body)),
		ContentLength:    int64(len(entry.Body)),
		TransferEncoding: nil,
		Close:            false,
		Uncompressed:     false,
		Trailer:          nil,
		Request:          req,
		TLS:              nil,
	}
}
