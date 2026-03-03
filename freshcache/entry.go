package freshcache

import (
	"net/http"
	"time"
)

// Entry represents a cached HTTP response.
type Entry struct {
	StatusCode   int
	Header       http.Header
	Body         []byte
	LastModified string
	CachedAt     time.Time
}

func (e *Entry) clone() *Entry {
	clone := *e
	clone.Header = e.Header.Clone()
	clone.Body = append([]byte(nil), e.Body...)
	return &clone
}
