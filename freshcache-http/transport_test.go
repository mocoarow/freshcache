package freshhttp_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mocoarow/freshcache"
	freshhttp "github.com/mocoarow/freshcache-http"
)

const testBodyJSON = `{"id":1}`

func newTestClient(t *testing.T, opts ...freshhttp.Option) *http.Client {
	t.Helper()
	cache := freshcache.NewMemoryCache()
	transport := freshhttp.NewTransport(http.DefaultTransport, cache, opts...)
	return &http.Client{Transport: transport}
}

func doGETAndRead(t *testing.T, client *http.Client, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err, "creating GET request should not fail")
	resp, err := client.Do(req) //nolint:gosec // G704: test helper, URL from httptest.Server
	require.NoError(t, err, "GET request should not fail")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading response body should not fail")
	require.NoError(t, resp.Body.Close(), "closing response body should not fail")
	return resp.StatusCode, string(body)
}

func TestTransport_RoundTrip_shouldFetchAndCache_whenCacheMiss(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(5*time.Minute))

	// when
	status, body := doGETAndRead(t, client, srv.URL+"/users/1")

	// then
	assert.Equal(t, http.StatusOK, status, "status should be 200")
	assert.JSONEq(t, testBodyJSON, body, "body should match server response")
	assert.Equal(t, int32(1), reqCount.Load(), "server should receive exactly 1 request")
}

func TestTransport_RoundTrip_shouldReturnCached_whenWithinTTL(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(5*time.Minute))

	// first request to populate cache
	doGETAndRead(t, client, srv.URL+"/users/1")

	// when - second request within TTL
	_, body := doGETAndRead(t, client, srv.URL+"/users/1")

	// then
	assert.JSONEq(t, testBodyJSON, body, "body should be served from cache")
	assert.Equal(t, int32(1), reqCount.Load(), "server should receive only 1 request (cache hit)")
}

func TestTransport_RoundTrip_shouldSendIfModifiedSinceAndReturn304_whenTTLExpiredAndNotModified(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		if r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(1*time.Nanosecond))

	// first request to populate cache
	doGETAndRead(t, client, srv.URL+"/users/1")

	// when - second request with expired TTL
	status, body := doGETAndRead(t, client, srv.URL+"/users/1")

	// then
	assert.Equal(t, http.StatusOK, status, "status should be 200 (from cache after 304)")
	assert.JSONEq(t, testBodyJSON, body, "body should be served from cache after 304")
	assert.Equal(t, int32(2), reqCount.Load(), "server should receive 2 requests (initial + conditional)")
}

func TestTransport_RoundTrip_shouldUpdateCache_whenTTLExpiredAndServerReturns200(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		w.Header().Set("Last-Modified", "Fri, 02 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if reqCount.Load() == 1 {
			if _, err := w.Write([]byte(`{"id":1,"v":"old"}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		} else {
			if _, err := w.Write([]byte(`{"id":1,"v":"new"}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(1*time.Nanosecond))

	// first request
	doGETAndRead(t, client, srv.URL+"/users/1")

	// when - second request, server returns new data
	_, body := doGETAndRead(t, client, srv.URL+"/users/1")

	// then
	assert.JSONEq(t, `{"id":1,"v":"new"}`, body, "body should be updated response from server")
}

func TestTransport_RoundTrip_shouldAlwaysReturnCached_whenTTLIsZero(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(0))

	// first request to populate cache
	doGETAndRead(t, client, srv.URL+"/users/1")

	// when - second request (should be served from cache)
	status, body := doGETAndRead(t, client, srv.URL+"/users/1")

	// then
	assert.Equal(t, http.StatusOK, status, "status should be 200 from cache")
	assert.JSONEq(t, testBodyJSON, body, "body should be served from cache with TTL=0")
	assert.Equal(t, int32(1), reqCount.Load(), "server should receive only 1 request (TTL=0 means unlimited)")
}

func TestTransport_RoundTrip_shouldNotCacheErrorResponse_whenCacheErrorsIsFalse(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		if reqCount.Load() == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte("error")); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(5*time.Minute), freshhttp.WithCacheErrors(false))

	// first request - error response
	doGETAndRead(t, client, srv.URL+"/data")

	// when - second request should hit server again (not cached)
	status, body := doGETAndRead(t, client, srv.URL+"/data")

	// then
	assert.Equal(t, http.StatusOK, status, "status should be 200 on retry after uncached error")
	assert.Equal(t, "ok", body, "body should be fresh response after uncached error")
	assert.Equal(t, int32(2), reqCount.Load(), "server should receive 2 requests (error not cached)")
}

func TestTransport_RoundTrip_shouldCacheErrorResponse_whenCacheErrorsIsTrue(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte("error")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(5*time.Minute), freshhttp.WithCacheErrors(true))

	// first request - error response (should be cached)
	doGETAndRead(t, client, srv.URL+"/data")

	// when - second request within TTL
	status, body := doGETAndRead(t, client, srv.URL+"/data")

	// then
	assert.Equal(t, http.StatusInternalServerError, status, "status should be 500 from cache")
	assert.Equal(t, "error", body, "body should be cached error response")
	assert.Equal(t, int32(1), reqCount.Load(), "server should receive only 1 request (error cached)")
}

func TestTransport_RoundTrip_shouldNotCache_whenBodyExceedsMaxBodySize(t *testing.T) {
	t.Parallel()

	// given
	largeBody := make([]byte, 1024)
	for i := range largeBody {
		largeBody[i] = 'A'
	}
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(largeBody); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(5*time.Minute), freshhttp.WithMaxBodySize(512))

	// when
	status, body := doGETAndRead(t, client, srv.URL+"/large")

	// then
	assert.Equal(t, http.StatusOK, status, "status should be 200")
	assert.Len(t, body, len(largeBody), "full body should be returned")

	// second request should hit server (not cached)
	doGETAndRead(t, client, srv.URL+"/large")
	assert.Equal(t, int32(2), reqCount.Load(), "server should receive 2 requests (not cached)")
}

func TestTransport_RoundTrip_shouldCache_whenBodyIsWithinMaxBodySize(t *testing.T) {
	t.Parallel()

	// given
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reqCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("small")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, freshhttp.WithTTL(5*time.Minute), freshhttp.WithMaxBodySize(512))

	// when
	doGETAndRead(t, client, srv.URL+"/small")
	status, body := doGETAndRead(t, client, srv.URL+"/small")

	// then
	assert.Equal(t, http.StatusOK, status, "status should be 200 from cache")
	assert.Equal(t, "small", body, "body should be served from cache")
	assert.Equal(t, int32(1), reqCount.Load(), "server should receive only 1 request (cached)")
}

func TestWithMaxBodySize_shouldPanic_whenNegative(t *testing.T) {
	t.Parallel()

	// when / then
	assert.Panics(t, func() {
		freshhttp.WithMaxBodySize(-1)
	}, "WithMaxBodySize(-1) should panic")
}

func TestTransport_RoundTrip_shouldNotCache_whenMethodIsNotGET(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
	}{
		{name: "POST", method: http.MethodPost},
		{name: "PUT", method: http.MethodPut},
		{name: "DELETE", method: http.MethodDelete},
		{name: "PATCH", method: http.MethodPatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given
			var reqCount atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reqCount.Add(1)
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte("ok")); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer srv.Close()

			client := newTestClient(t, freshhttp.WithTTL(5*time.Minute))

			// when - two requests
			for range 2 {
				req, err := http.NewRequestWithContext(context.Background(), tt.method, srv.URL+"/data", nil)
				require.NoError(t, err, "creating request should not fail")
				resp, err := client.Do(req) //nolint:gosec // G704: test code, URL from httptest.Server
				require.NoError(t, err, "request should not fail")
				require.NoError(t, resp.Body.Close(), "closing body should not fail")
			}

			// then - both should hit server
			assert.Equal(t, int32(2), reqCount.Load(), "%s requests should not be cached", tt.method)
		})
	}
}
