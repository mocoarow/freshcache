# freshcache

Go の HTTP キャッシュライブラリ。`Last-Modified` / `If-Modified-Since` ヘッダによるキャッシュを HTTP クライアントとサーバーの両方に提供する。

## モジュール

| モジュール | パッケージ | 説明 |
|---|---|---|
| [`freshcache`](./freshcache) | `freshcache` | コアインターフェースとインメモリキャッシュ |
| [`freshcache-http`](./freshcache-http) | `freshhttp` | クライアント側キャッシュ `http.RoundTripper` |
| [`freshcache-server`](./freshcache-server) | `freshserver` | サーバー側キャッシュ `http.Handler` ミドルウェア |

## インストール

```bash
# コアのみ
go get github.com/mocoarow/freshcache

# クライアント側キャッシュ
go get github.com/mocoarow/freshcache-http

# サーバー側キャッシュ
go get github.com/mocoarow/freshcache-server
```

## クライアント側の使い方 (freshcache-http)

`http.RoundTripper` をラップして GET レスポンスをキャッシュする。

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

### クライアントキャッシュの流れ

1. GET 以外のリクエスト → キャッシュせずそのまま委譲
2. キャッシュヒット & TTL 内 → キャッシュから返却
3. TTL 超過 & `Last-Modified` あり → `If-Modified-Since` 付きで再リクエスト
4. サーバーが 304 → キャッシュの TTL をリセットして返却
5. サーバーが 200 → キャッシュを更新して返却
6. エラーレスポンス (4xx/5xx) → `WithCacheErrors(true)` の場合のみキャッシュ

### クライアントオプション

| オプション | デフォルト | 説明 |
|---|---|---|
| `WithTTL(d)` | `0` (無期限キャッシュ) | キャッシュの有効期間 |
| `WithCacheErrors(v)` | `false` | エラーレスポンスをキャッシュするか |

## サーバー側の使い方 (freshcache-server)

`http.Handler` をラップして GET レスポンスをキャッシュする。

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

### WithLastModified — データ鮮度チェック

TTL 超過時にハンドラを呼ばず、データの実際の更新日時で鮮度を判断できる。

```go
mw := freshserver.NewMiddleware(cache, 5*time.Minute,
    freshserver.WithLastModified(func(r *http.Request) (time.Time, error) {
        return db.GetUpdatedAt(r.Context(), r.URL.Path)
    }),
)
```

データが更新されていなければ、ハンドラを呼ばずキャッシュから返却（または 304）する。

### サーバーキャッシュの流れ

1. GET 以外のリクエスト → そのまま次のハンドラに委譲
2. キャッシュヒット & TTL 内 → キャッシュから返却（`If-Modified-Since` が一致すれば 304）
3. キャッシュミス or TTL 超過:
   - `WithLastModified` 未設定 → 次のハンドラを呼び出し、レスポンスをキャッシュ
   - `WithLastModified` 設定済み & データ未更新 → キャッシュから返却（または 304）
   - `WithLastModified` 設定済み & データ更新済み → 次のハンドラを呼び出し、キャッシュ更新
   - `WithLastModified` がエラーを返す → フォールバックとしてハンドラを呼び出し

### サーバーオプション

| オプション | デフォルト | 説明 |
|---|---|---|
| `WithLastModified(fn)` | `nil` | データの更新日時を返す関数。データ未更新時にハンドラ呼び出しを回避する |

## カスタム Cache 実装

`freshcache.Cache` インターフェースを実装すれば、Redis 等の外部ストアも利用可能。実装はスレッドセーフであること。

```go
type Cache interface {
    Get(key string) (*Entry, bool)
    Set(key string, entry *Entry)
    Delete(key string)
}
```

## サンプル

[`examples/`](./examples) に実行可能なデモがある。

| サンプル | 説明 |
|---|---|
| [`server`](./examples/server) | TTL ベースのサーバー側キャッシュ |
| [`client`](./examples/client) | TTL ベースのクライアント側キャッシュ |
| [`server-lastmod`](./examples/server-lastmod) | `WithLastModified` を使ったサーバー側キャッシュ |
| [`client-lastmod`](./examples/client-lastmod) | `server-lastmod` 用のクライアント |

```bash
# LastModified サンプルの実行
go run ./examples/server-lastmod &
go run ./examples/client-lastmod
```

## 開発

[Task](https://taskfile.dev/) が必要。

```bash
task test                          # 全モジュールのテスト
task test:one MODULE=freshcache    # 特定モジュールのテスト
task lint                          # 全モジュールの golangci-lint
task vet                           # 全モジュールの go vet
task fmt                           # 全モジュールの goimports
task check                         # 全チェック実行 (vet, lint, test)
```
