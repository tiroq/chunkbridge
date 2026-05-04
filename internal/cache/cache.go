// Package cache implements a conservative, bounded, in-memory HTTP response
// cache for the chunkbridge client/proxy side.
//
// Only safe GET and HEAD responses are stored. Responses containing
// Set-Cookie, Cache-Control: private/no-store, or Vary: * are never cached.
// The cache is disabled by default and must be explicitly enabled via config.
//
// Stale-while-revalidate and conditional GET (ETag/If-Modified-Since) are not
// implemented; stale entries are evicted on access.
package cache

import (
	"net/http"
	"sync"
	"time"
)

// Clock is an injectable time source, used to control time in tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Entry is a cached HTTP response. All fields are immutable after insertion.
type Entry struct {
	// Method and URL identify the original request.
	Method string
	URL    string
	// StatusCode, Header, and Body are the cached response values.
	StatusCode int
	Header     http.Header // defensive copy; never mutate
	Body       []byte
	// StoredAt is when the entry was written.
	StoredAt time.Time
	// ExpiresAt is when the entry becomes stale.
	ExpiresAt time.Time
	// ETag and LastModified are recorded for future revalidation support.
	ETag         string
	LastModified string
	// BodySize is len(Body); tracked separately for accounting.
	BodySize int
}

// Config carries capacity and behaviour limits for the cache.
// It mirrors config.CacheConfig but lives here to avoid circular imports.
type Config struct {
	MaxEntries        int
	MaxBytes          int64
	MaxEntryBytes     int64
	DefaultTTLSeconds int
	CachePrivate      bool
	CacheWithCookies  bool
	// CacheWithAuthorization allows caching requests that carry Authorization.
	CacheWithAuthorization bool
	// Clock is an optional time source; defaults to the real wall clock.
	Clock Clock
}

// Cache is a thread-safe, bounded LRU in-memory HTTP response cache.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]*Entry
	lru        *lruList
	totalBytes int64
	cfg        Config
	clock      Clock
}

// New creates a new Cache with the given limits.
func New(cfg Config) *Cache {
	clk := cfg.Clock
	if clk == nil {
		clk = realClock{}
	}
	return &Cache{
		entries: make(map[string]*Entry, cfg.MaxEntries),
		lru:     newLRUList(),
		cfg:     cfg,
		clock:   clk,
	}
}

// Get looks up key in the cache. Returns (entry, true) if the entry exists and
// is still fresh, or (nil, false) otherwise. Stale entries are evicted lazily.
func (c *Cache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.clock.Now().After(e.ExpiresAt) {
		c.drop(key)
		return nil, false
	}
	c.lru.touch(key)
	return e, true
}

// Put stores e under key. Returns without storing if the entry's body exceeds
// MaxEntryBytes. Evicts the least-recently-used entry as needed to satisfy
// MaxEntries and MaxBytes constraints. If key already exists, the old entry is
// replaced.
func (c *Cache) Put(key string, e *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entryBytes := int64(len(e.Body))
	if c.cfg.MaxEntryBytes > 0 && entryBytes > c.cfg.MaxEntryBytes {
		return
	}

	// Replace any existing entry with the same key.
	if old, ok := c.entries[key]; ok {
		c.totalBytes -= int64(old.BodySize)
		c.lru.remove(key)
		delete(c.entries, key)
	}

	// Evict LRU entries until limits are satisfied.
	for {
		tooMany := c.cfg.MaxEntries > 0 && len(c.entries) >= c.cfg.MaxEntries
		tooBig := c.cfg.MaxBytes > 0 && c.totalBytes+entryBytes > c.cfg.MaxBytes
		if !tooMany && !tooBig {
			break
		}
		oldest := c.lru.evictOldest()
		if oldest == "" {
			break
		}
		if old, ok := c.entries[oldest]; ok {
			c.totalBytes -= int64(old.BodySize)
			delete(c.entries, oldest)
		}
	}

	e.BodySize = int(entryBytes)
	// Defensive copy: avoid aliasing the caller's slice.
	bodyCopy := make([]byte, len(e.Body))
	copy(bodyCopy, e.Body)
	stored := *e
	stored.Body = bodyCopy
	c.entries[key] = &stored
	c.lru.add(key)
	c.totalBytes += entryBytes
}

// Len returns the number of entries currently stored.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// TotalBytes returns the sum of all stored body sizes in bytes.
func (c *Cache) TotalBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalBytes
}

// drop removes key from the cache. Must be called with mu held.
func (c *Cache) drop(key string) {
	if e, ok := c.entries[key]; ok {
		c.totalBytes -= int64(e.BodySize)
		delete(c.entries, key)
	}
	c.lru.remove(key)
}
