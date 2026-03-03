package freshserver_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mocoarow/freshcache"
	freshserver "github.com/mocoarow/freshcache-server"
)

const (
	testBodyJSON = `{"id":1}`
	testBodyNew  = "new"
)

func serveAndRead(t *testing.T, handler http.Handler, method, path string, headers http.Header) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err, "reading response body should not fail")
	return rec.Code, string(body)
}

func TestMiddleware_Wrap_shouldCallNextAndCache_whenCacheMiss(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 5*time.Minute).Wrap(next)

	// when
	code, body := serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200")
	assert.JSONEq(t, testBodyJSON, body, "body should match handler response")
	assert.Equal(t, int32(1), callCount.Load(), "handler should be called once")
}

func TestMiddleware_Wrap_shouldReturnCached_whenWithinTTL(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 5*time.Minute).Wrap(next)

	// first request to populate cache
	serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// when - second request within TTL
	code, body := serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200 from cache")
	assert.JSONEq(t, testBodyJSON, body, "body should be served from cache")
	assert.Equal(t, int32(1), callCount.Load(), "handler should be called only once (cache hit)")
}

func TestMiddleware_Wrap_shouldReturn304_whenIfModifiedSinceIsAfterLastModified(t *testing.T) {
	t.Parallel()

	// given
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 5*time.Minute).Wrap(next)

	// first request to populate cache
	serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// when - request with If-Modified-Since after Last-Modified
	ims := http.Header{"If-Modified-Since": {"Fri, 02 Jan 2025 00:00:00 GMT"}}
	code, _ := serveAndRead(t, handler, http.MethodGet, "/users/1", ims)

	// then
	assert.Equal(t, http.StatusNotModified, code, "status should be 304 when IMS is after Last-Modified")
}

func TestMiddleware_Wrap_shouldCallNextAgain_whenTTLExpired(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if callCount.Load() == 1 {
			if _, err := w.Write([]byte("old")); err != nil {
				t.Errorf("write response: %v", err)
			}
		} else {
			if _, err := w.Write([]byte(testBodyNew)); err != nil {
				t.Errorf("write response: %v", err)
			}
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 1*time.Nanosecond).Wrap(next)

	// first request
	serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// when - second request with expired TTL
	code, body := serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200")
	assert.Equal(t, testBodyNew, body, "body should be fresh response after TTL expired")
	assert.Equal(t, int32(2), callCount.Load(), "handler should be called twice")
}

func TestMiddleware_Wrap_shouldNotCache_whenMethodIsNotGET(t *testing.T) {
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
			var callCount atomic.Int32
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				callCount.Add(1)
				w.WriteHeader(http.StatusOK)
				if _, err := w.Write([]byte("ok")); err != nil {
					t.Errorf("write response: %v", err)
				}
			})

			cache := freshcache.NewMemoryCache()
			handler := freshserver.NewMiddleware(cache, 5*time.Minute).Wrap(next)

			// when - two requests
			serveAndRead(t, handler, tt.method, "/data", nil)
			serveAndRead(t, handler, tt.method, "/data", nil)

			// then - both should call next
			assert.Equal(t, int32(2), callCount.Load(), "%s requests should not be cached", tt.method)
		})
	}
}

func TestMiddleware_Wrap_shouldAlwaysReturnCached_whenTTLIsZero(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("cached")); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 0).Wrap(next)

	// first request to populate cache
	serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// when - second request (should be served from cache)
	code, body := serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200 from cache")
	assert.Equal(t, "cached", body, "body should be served from cache with TTL=0")
	assert.Equal(t, int32(1), callCount.Load(), "handler should be called only once (TTL=0 means unlimited)")
}

func TestMiddleware_Wrap_shouldSetLastModified_whenNextDoesNotProvideIt(t *testing.T) {
	t.Parallel()

	// given
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("no-lm")); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 5*time.Minute).Wrap(next)

	// when
	req := httptest.NewRequest(http.MethodGet, "/data", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// then
	lm := rec.Header().Get("Last-Modified")
	assert.NotEmpty(t, lm, "Last-Modified header should be auto-set")
	_, err := http.ParseTime(lm)
	assert.NoError(t, err, "Last-Modified should be a valid HTTP date")
}

func TestMiddleware_Wrap_shouldReturnCached_whenLastModifiedFuncReturnsOldTime(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	dataModTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	cache := freshcache.NewMemoryCache()
	mw := freshserver.NewMiddleware(cache, 1*time.Nanosecond,
		freshserver.WithLastModified(func(_ *http.Request) (time.Time, error) {
			return dataModTime, nil
		}),
	)
	handler := mw.Wrap(next)

	// first request to populate cache
	serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// when - second request after TTL expired, but data not modified
	code, body := serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200 from stale cache")
	assert.JSONEq(t, testBodyJSON, body, "body should be served from cache (data unchanged)")
	assert.Equal(t, int32(1), callCount.Load(), "handler should be called only once (data not modified)")
}

func TestMiddleware_Wrap_shouldReturn304_whenLastModifiedFuncReturnsOldTimeAndClientSendsIMS(t *testing.T) {
	t.Parallel()

	// given
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	dataModTime := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	cache := freshcache.NewMemoryCache()
	mw := freshserver.NewMiddleware(cache, 1*time.Nanosecond,
		freshserver.WithLastModified(func(_ *http.Request) (time.Time, error) {
			return dataModTime, nil
		}),
	)
	handler := mw.Wrap(next)

	// first request to populate cache
	serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// when - second request with IMS header after TTL expired
	ims := http.Header{"If-Modified-Since": {"Fri, 02 Jan 2025 00:00:00 GMT"}}
	code, _ := serveAndRead(t, handler, http.MethodGet, "/users/1", ims)

	// then
	assert.Equal(t, http.StatusNotModified, code, "status should be 304 when data unchanged and IMS matches")
}

func TestMiddleware_Wrap_shouldCallNext_whenLastModifiedFuncReturnsNewTime(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if callCount.Load() == 1 {
			if _, err := w.Write([]byte("old")); err != nil {
				t.Errorf("write response: %v", err)
			}
		} else {
			if _, err := w.Write([]byte(testBodyNew)); err != nil {
				t.Errorf("write response: %v", err)
			}
		}
	})

	dataModTime := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	cache := freshcache.NewMemoryCache()
	mw := freshserver.NewMiddleware(cache, 1*time.Nanosecond,
		freshserver.WithLastModified(func(_ *http.Request) (time.Time, error) {
			return dataModTime, nil
		}),
	)
	handler := mw.Wrap(next)

	// first request to populate cache
	serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// when - second request after TTL expired, data is newer
	code, body := serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200")
	assert.Equal(t, testBodyNew, body, "body should be fresh response (data changed)")
	assert.Equal(t, int32(2), callCount.Load(), "handler should be called twice (data modified)")
}

func TestMiddleware_Wrap_shouldFallbackToNext_whenLastModifiedFuncReturnsError(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if callCount.Load() == 1 {
			if _, err := w.Write([]byte("old")); err != nil {
				t.Errorf("write response: %v", err)
			}
		} else {
			if _, err := w.Write([]byte(testBodyNew)); err != nil {
				t.Errorf("write response: %v", err)
			}
		}
	})

	cache := freshcache.NewMemoryCache()
	mw := freshserver.NewMiddleware(cache, 1*time.Nanosecond,
		freshserver.WithLastModified(func(_ *http.Request) (time.Time, error) {
			return time.Time{}, errors.New("db connection failed")
		}),
	)
	handler := mw.Wrap(next)

	// first request to populate cache
	serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// when - second request after TTL expired, LastModifiedFunc returns error
	code, body := serveAndRead(t, handler, http.MethodGet, "/data", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200 on fallback")
	assert.Equal(t, testBodyNew, body, "body should be fresh response (fallback to handler)")
	assert.Equal(t, int32(2), callCount.Load(), "handler should be called twice (fallback on error)")
}

func TestMiddleware_Wrap_shouldPassThroughWithoutCaching_whenBodyExceedsMaxBodySize(t *testing.T) {
	t.Parallel()

	// given
	largeBody := make([]byte, 1024)
	for i := range largeBody {
		largeBody[i] = 'A'
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(largeBody); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 5*time.Minute,
		freshserver.WithMaxBodySize(512),
	).Wrap(next)

	// when
	code, body := serveAndRead(t, handler, http.MethodGet, "/large", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200")
	assert.Len(t, body, len(largeBody), "full body should be sent to client via pass-through")
	assert.Equal(t, 0, cache.Len(), "response should not be cached when body exceeds maxBodySize")
}

func TestMiddleware_Wrap_shouldCache_whenBodyIsWithinMaxBodySize(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("small")); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	handler := freshserver.NewMiddleware(cache, 5*time.Minute,
		freshserver.WithMaxBodySize(512),
	).Wrap(next)

	// when
	serveAndRead(t, handler, http.MethodGet, "/small", nil)
	code, body := serveAndRead(t, handler, http.MethodGet, "/small", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200 from cache")
	assert.Equal(t, "small", body, "body should be served from cache")
	assert.Equal(t, int32(1), callCount.Load(), "handler should be called only once (cached)")
}

func TestWithMaxBodySize_shouldPanic_whenNegative(t *testing.T) {
	t.Parallel()

	// when / then
	assert.Panics(t, func() {
		freshserver.WithMaxBodySize(-1)
	}, "WithMaxBodySize(-1) should panic")
}

func TestMiddleware_Wrap_shouldCallNext_whenLastModifiedFuncSetButNoCacheEntry(t *testing.T) {
	t.Parallel()

	// given
	var callCount atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.Header().Set("Last-Modified", "Thu, 01 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(testBodyJSON)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})

	cache := freshcache.NewMemoryCache()
	mw := freshserver.NewMiddleware(cache, 5*time.Minute,
		freshserver.WithLastModified(func(_ *http.Request) (time.Time, error) {
			return time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC), nil
		}),
	)
	handler := mw.Wrap(next)

	// when - first request, no cache entry exists
	code, body := serveAndRead(t, handler, http.MethodGet, "/users/1", nil)

	// then
	assert.Equal(t, http.StatusOK, code, "status should be 200")
	assert.JSONEq(t, testBodyJSON, body, "body should match handler response")
	assert.Equal(t, int32(1), callCount.Load(), "handler should be called once (cache miss)")
}
