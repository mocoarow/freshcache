// Package freshserver provides server-side HTTP caching middleware.
package freshserver

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mocoarow/freshcache"
)

const defaultMaxBodySize int64 = 10 * 1024 * 1024 // 10 MB

// responseRecorder captures an HTTP response for caching.
// When the response body exceeds maxBodySize, it switches to pass-through mode
// and writes directly to the underlying http.ResponseWriter.
type responseRecorder struct {
	code          int
	header        http.Header
	body          bytes.Buffer
	maxBodySize   int64
	overflowed    bool
	underlying    http.ResponseWriter
	headerFlushed bool
}

func newResponseRecorder(maxBodySize int64, w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		code:          http.StatusOK,
		header:        make(http.Header),
		body:          bytes.Buffer{},
		maxBodySize:   maxBodySize,
		overflowed:    false,
		underlying:    w,
		headerFlushed: false,
	}
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) WriteHeader(code int) {
	r.code = code
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.overflowed {
		n, err := r.underlying.Write(b)
		if err != nil {
			return n, fmt.Errorf("write underlying response: %w", err)
		}
		return n, nil
	}
	if r.maxBodySize > 0 && int64(r.body.Len()+len(b)) > r.maxBodySize {
		r.overflowed = true
		r.flushHeader()
		if r.body.Len() > 0 {
			//nolint:errcheck,gosec
			r.underlying.Write(r.body.Bytes())
			r.body.Reset()
		}
		n, err := r.underlying.Write(b)
		if err != nil {
			return n, fmt.Errorf("write underlying response: %w", err)
		}
		return n, nil
	}
	n, err := r.body.Write(b)
	if err != nil {
		return n, fmt.Errorf("write response buffer: %w", err)
	}
	return n, nil
}

func (r *responseRecorder) flushHeader() {
	if r.headerFlushed {
		return
	}
	r.headerFlushed = true
	for k, vs := range r.header {
		for _, v := range vs {
			r.underlying.Header().Add(k, v)
		}
	}
	r.underlying.WriteHeader(r.code)
}

// LastModifiedFunc returns the last modification time for the given request.
// Middleware calls this when TTL has expired (or no cache entry exists) to check data freshness.
type LastModifiedFunc func(r *http.Request) (time.Time, error)

// Option configures the Middleware.
type Option func(*Middleware)

// WithLastModified sets a function that returns the last modification time of the data.
// When set, the Middleware uses this to avoid calling the downstream handler if the data
// has not changed since the cached response was stored.
func WithLastModified(fn LastModifiedFunc) Option {
	return func(m *Middleware) { m.lastModified = fn }
}

// WithMaxBodySize sets the maximum response body size that will be cached.
// Responses larger than this are passed through without caching.
// Default is 10 MB. A value of 0 means no limit. Negative values will panic.
func WithMaxBodySize(n int64) Option {
	if n < 0 {
		panic("freshserver: maxBodySize must not be negative")
	}
	return func(m *Middleware) { m.maxBodySize = n }
}

// Middleware caches HTTP GET responses from downstream handlers.
type Middleware struct {
	cache        freshcache.Cache
	ttl          time.Duration
	lastModified LastModifiedFunc
	maxBodySize  int64
}

// NewMiddleware creates a new caching Middleware.
// cache must not be nil; passing nil will panic.
func NewMiddleware(cache freshcache.Cache, ttl time.Duration, opts ...Option) *Middleware {
	if cache == nil {
		panic("freshserver: cache must not be nil")
	}
	m := &Middleware{
		cache:        cache,
		ttl:          ttl,
		lastModified: nil,
		maxBodySize:  defaultMaxBodySize,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Wrap returns an http.Handler that caches GET responses from next.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}

		key := r.URL.String()
		entry, ok := m.cache.Get(key)

		if ok && m.isWithinTTL(entry) {
			m.serveCached(w, r, entry)
			return
		}

		if ok && m.tryServeStale(w, r, key, entry) {
			return
		}

		m.fetchAndCache(w, r, next, key)
	})
}

func (m *Middleware) isWithinTTL(entry *freshcache.Entry) bool {
	return m.ttl == 0 || time.Since(entry.CachedAt) < m.ttl
}

func (m *Middleware) serveCached(w http.ResponseWriter, r *http.Request, entry *freshcache.Entry) {
	lmTime, err := http.ParseTime(entry.LastModified)
	if err == nil && respondNotModified(w, r, lmTime) {
		return
	}
	writeEntry(w, entry)
}

func respondNotModified(w http.ResponseWriter, r *http.Request, lastModified time.Time) bool {
	ims := r.Header.Get("If-Modified-Since")
	if ims == "" {
		return false
	}
	imsTime, err := http.ParseTime(ims)
	if err != nil {
		return false
	}
	if lastModified.After(imsTime) {
		return false
	}
	w.WriteHeader(http.StatusNotModified)
	return true
}

func (m *Middleware) tryServeStale(w http.ResponseWriter, r *http.Request, key string, entry *freshcache.Entry) bool {
	if m.lastModified == nil {
		return false
	}
	dataModTime, err := m.lastModified(r)
	if err != nil {
		slog.Warn("last modified check failed, falling back to fetch", //nolint:gosec // G706: structured slog, not format string
			"url", r.URL.String(), "error", err)
		return false
	}
	entryLM, err := http.ParseTime(entry.LastModified)
	if err != nil {
		return false
	}
	if dataModTime.After(entryLM) {
		return false
	}
	refreshed := &freshcache.Entry{
		StatusCode:   entry.StatusCode,
		Header:       entry.Header,
		Body:         entry.Body,
		LastModified: entry.LastModified,
		CachedAt:     time.Now(),
	}
	m.cache.Set(key, refreshed)

	if respondNotModified(w, r, dataModTime) {
		return true
	}
	writeEntry(w, refreshed)
	return true
}

func (m *Middleware) fetchAndCache(w http.ResponseWriter, r *http.Request, next http.Handler, key string) {
	rec := newResponseRecorder(m.maxBodySize, w)
	next.ServeHTTP(rec, r)

	if rec.overflowed {
		return
	}

	lm := rec.header.Get("Last-Modified")
	if lm == "" {
		lm = m.resolveLastModified(r)
		rec.header.Set("Last-Modified", lm)
	}

	entry := &freshcache.Entry{
		StatusCode:   rec.code,
		Header:       rec.header.Clone(),
		Body:         rec.body.Bytes(),
		LastModified: lm,
		CachedAt:     time.Now(),
	}

	m.cache.Set(key, entry)
	writeEntry(w, entry)
}

func (m *Middleware) resolveLastModified(r *http.Request) string {
	if m.lastModified != nil {
		dataModTime, err := m.lastModified(r)
		if err != nil {
			slog.Warn("resolve last modified failed, using current time", //nolint:gosec // G706: structured slog, not format string
				"url", r.URL.String(), "error", err)
		} else {
			return dataModTime.UTC().Format(http.TimeFormat)
		}
	}
	return time.Now().UTC().Format(http.TimeFormat)
}

func writeEntry(w http.ResponseWriter, entry *freshcache.Entry) {
	for k, vs := range entry.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(entry.StatusCode)
	// Error is intentionally not checked: the only possible failure is if the
	// client disconnected, and there is no meaningful recovery in an HTTP handler.
	//nolint:errcheck,gosec
	w.Write(entry.Body)
}
