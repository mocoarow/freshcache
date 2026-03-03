module github.com/mocoarow/freshcache/examples

go 1.25.7

require (
	github.com/mocoarow/freshcache v0.0.0
	github.com/mocoarow/freshcache-http v0.0.0
	github.com/mocoarow/freshcache-server v0.0.0
)

replace (
	github.com/mocoarow/freshcache => ../freshcache
	github.com/mocoarow/freshcache-http => ../freshcache-http
	github.com/mocoarow/freshcache-server => ../freshcache-server
)
