# freshcache

HTTP caching library for Go. Provides caching via `Last-Modified` / `If-Modified-Since` headers for both HTTP clients and servers.

## Modules

| Module | Package | Description |
|---|---|---|
| [`freshcache`](./freshcache) | `freshcache` | Core interfaces and in-memory cache |
| [`freshcache-http`](./freshcache-http) | `freshhttp` | Client-side caching `http.RoundTripper` |
| [`freshcache-server`](./freshcache-server) | `freshserver` | Server-side caching `http.Handler` middleware |

## Installation

```bash
# Core only
go get github.com/mocoarow/freshcache

# Client-side caching
go get github.com/mocoarow/freshcache-http

# Server-side caching
go get github.com/mocoarow/freshcache-server
```

## Client-side Usage (freshcache-http)

Wraps an `http.RoundTripper` to cache GET responses.

```go
import (
    "net/http"
    "time"

    "github.com/mocoarow/freshcache"
    freshhttp "github.com/mocoarow/freshcache-http"
)

cache := freshcache.NewMemoryCache()
transport := freshhttp.NewTransport(
    http.DefaultTransport,
    cache,
    freshhttp.WithTTL(5*time.Minute),
    freshhttp.WithCacheErrors(false),
)
client := &http.Client{Transport: transport}

resp, err := client.Get("https://api.example.com/users/1")
```

### How Client Caching Works

1. Non-GET requests → passed through without caching
2. Cache hit & within TTL → serve from cache
3. TTL expired & `Last-Modified` present → send `If-Modified-Since` header
4. Server returns 304 → reset TTL and serve from cache
5. Server returns 200 → update cache and serve
6. Error responses (4xx/5xx) → cached only if `WithCacheErrors(true)`

### Client Options

| Option | Default | Description |
|---|---|---|
| `WithTTL(d)` | `0` (cache indefinitely) | Cache time-to-live |
| `WithCacheErrors(v)` | `false` | Whether to cache error responses |

## Server-side Usage (freshcache-server)

Wraps an `http.Handler` to cache GET responses.

```go
import (
    "net/http"
    "time"

    "github.com/mocoarow/freshcache"
    freshserver "github.com/mocoarow/freshcache-server"
)

cache := freshcache.NewMemoryCache()
mw := freshserver.NewMiddleware(cache, 5*time.Minute)

mux := http.NewServeMux()
mux.HandleFunc("/api/data", handleData)

http.ListenAndServe(":8080", mw.Wrap(mux))
```

### WithLastModified — Data Freshness Check

When TTL expires, you can avoid calling the handler by providing a function that returns the data's actual modification time.

```go
mw := freshserver.NewMiddleware(cache, 5*time.Minute,
    freshserver.WithLastModified(func(r *http.Request) (time.Time, error) {
        return db.GetUpdatedAt(r.Context(), r.URL.Path)
    }),
)
```

If the data hasn't changed since the cached response, the middleware serves from cache (or returns 304) without calling the handler.

### How Server Caching Works

1. Non-GET requests → passed through to the next handler
2. Cache hit & within TTL → serve from cache (304 if `If-Modified-Since` matches)
3. Cache miss or TTL expired:
   - `WithLastModified` not set → call next handler, cache the response
   - `WithLastModified` set & data unchanged → serve from cache (or 304)
   - `WithLastModified` set & data changed → call next handler, cache the response
   - `WithLastModified` returns error → fall back to calling next handler

### Server Options

| Option | Default | Description |
|---|---|---|
| `WithLastModified(fn)` | `nil` | Function returning data modification time; avoids handler calls when data is unchanged |

## Custom Cache Implementation

Implement the `freshcache.Cache` interface to use external stores like Redis. Implementations must be safe for concurrent use.

```go
type Cache interface {
    Get(key string) (*Entry, bool)
    Set(key string, entry *Entry)
    Delete(key string)
}
```

## Examples

See [`examples/`](./examples) for runnable demos.

| Example | Description |
|---|---|
| [`server`](./examples/server) | Basic server-side caching with TTL |
| [`client`](./examples/client) | Basic client-side caching with TTL |
| [`server-lastmod`](./examples/server-lastmod) | Server-side caching with `WithLastModified` |
| [`client-lastmod`](./examples/client-lastmod) | Client for the `server-lastmod` example |

```bash
# Run the LastModified example
go run ./examples/server-lastmod &
go run ./examples/client-lastmod
```

## Development

Requires [Task](https://taskfile.dev/).

```bash
task test                          # Run tests for all modules
task test:one MODULE=freshcache    # Run tests for a specific module
task lint                          # Run golangci-lint for all modules
task vet                           # Run go vet for all modules
task fmt                           # Run goimports for all modules
task check                         # Run all checks (vet, lint, test)
```
