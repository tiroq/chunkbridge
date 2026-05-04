package cache_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/cache"
)

// ─── fake clock ───────────────────────────────────────────────────────────────

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

// ─── helpers ──────────────────────────────────────────────────────────────────

func newTestCache(maxEntries int, maxBytes, maxEntryBytes int64, clk cache.Clock) *cache.Cache {
	return cache.New(cache.Config{
		MaxEntries:    maxEntries,
		MaxBytes:      maxBytes,
		MaxEntryBytes: maxEntryBytes,
		Clock:         clk,
	})
}

func makeEntry(method, url string, status int, body []byte, ttl time.Duration, clk cache.Clock) *cache.Entry {
	now := clk.Now()
	hdr := make(http.Header)
	hdr.Set("Content-Type", "text/plain")
	return &cache.Entry{
		Method:     method,
		URL:        url,
		StatusCode: status,
		Header:     hdr,
		Body:       body,
		StoredAt:   now,
		ExpiresAt:  now.Add(ttl),
	}
}

func makeGETRequest(url string, extraHeaders ...http.Header) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, url, nil)
	if len(extraHeaders) > 0 {
		for k, vs := range extraHeaders[0] {
			for _, v := range vs {
				r.Header.Add(k, v)
			}
		}
	}
	return r
}

// ─── TestCacheStoresAndReturnsFreshGET ────────────────────────────────────────

func TestCacheStoresAndReturnsFreshGET(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	c := newTestCache(10, 1024, 512, clk)

	body := []byte("hello")
	key := cache.BuildKey("GET", "http://example.com/foo", nil)
	e := makeEntry("GET", "http://example.com/foo", 200, body, 60*time.Second, clk)
	c.Put(key, e)

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("expected cache HIT, got MISS")
	}
	if got.StatusCode != 200 {
		t.Errorf("status: want 200, got %d", got.StatusCode)
	}
	if string(got.Body) != "hello" {
		t.Errorf("body: want %q, got %q", "hello", got.Body)
	}
}

// ─── TestCacheDoesNotStorePOST ────────────────────────────────────────────────

func TestCacheDoesNotStorePOST(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "http://example.com/api", nil)
	ok := cache.IsResponseCacheable("POST", 200, make(http.Header), 0, cache.ResponseOptions{MaxEntryBytes: 1024})
	if ok {
		t.Error("POST response must not be cacheable")
	}
	opts := cache.RequestOptions{}
	if cache.IsRequestCacheable(r, opts) {
		t.Error("POST request must not be cacheable")
	}
}

// ─── TestCacheBypassesAuthorizationByDefault ──────────────────────────────────

func TestCacheBypassesAuthorizationByDefault(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "http://example.com/secure", nil)
	r.Header.Set("Authorization", "Bearer tok")
	opts := cache.RequestOptions{CacheWithAuthorization: false}
	if cache.IsRequestCacheable(r, opts) {
		t.Error("request with Authorization header must not be cacheable when CacheWithAuthorization=false")
	}
}

// ─── TestCacheBypassesCookieByDefault ─────────────────────────────────────────

func TestCacheBypassesCookieByDefault(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "http://example.com/page", nil)
	r.Header.Set("Cookie", "session=abc")
	opts := cache.RequestOptions{CacheWithCookies: false}
	if cache.IsRequestCacheable(r, opts) {
		t.Error("request with Cookie header must not be cacheable when CacheWithCookies=false")
	}
}

// ─── TestCacheDoesNotStoreSetCookieByDefault ───────────────────────────────────

func TestCacheDoesNotStoreSetCookieByDefault(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Set-Cookie", "session=xyz; Path=/")
	ok := cache.IsResponseCacheable("GET", 200, hdr, 0, cache.ResponseOptions{CachePrivate: false})
	if ok {
		t.Error("response with Set-Cookie must not be cacheable when CachePrivate=false")
	}
}

// ─── TestCacheDoesNotStoreNoStore ─────────────────────────────────────────────

func TestCacheDoesNotStoreNoStore(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Cache-Control", "no-store")
	ok := cache.IsResponseCacheable("GET", 200, hdr, 0, cache.ResponseOptions{MaxEntryBytes: 1024})
	if ok {
		t.Error("response with Cache-Control: no-store must not be cacheable")
	}
}

// ─── TestCacheDoesNotStorePrivateByDefault ────────────────────────────────────

func TestCacheDoesNotStorePrivateByDefault(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Cache-Control", "private")
	ok := cache.IsResponseCacheable("GET", 200, hdr, 0, cache.ResponseOptions{CachePrivate: false})
	if ok {
		t.Error("response with Cache-Control: private must not be cacheable when CachePrivate=false")
	}
}

// ─── TestCacheDoesNotStoreVaryStar ────────────────────────────────────────────

func TestCacheDoesNotStoreVaryStar(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Vary", "*")
	ok := cache.IsResponseCacheable("GET", 200, hdr, 0, cache.ResponseOptions{MaxEntryBytes: 1024})
	if ok {
		t.Error("response with Vary: * must not be cacheable")
	}
}

// ─── TestCacheMaxEntryBytes ───────────────────────────────────────────────────

func TestCacheMaxEntryBytes(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	c := newTestCache(10, 1024, 5, clk) // max entry = 5 bytes

	key := cache.BuildKey("GET", "http://example.com/big", nil)
	e := makeEntry("GET", "http://example.com/big", 200, []byte("toolarge"), 60*time.Second, clk)
	c.Put(key, e) // body = 8 bytes > 5 bytes limit → not stored

	if _, ok := c.Get(key); ok {
		t.Error("entry exceeding MaxEntryBytes must not be stored")
	}
}

// ─── TestCacheEvictsMaxEntries ────────────────────────────────────────────────

func TestCacheEvictsMaxEntries(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	c := newTestCache(2, 1024, 512, clk) // max 2 entries

	for i, url := range []string{
		"http://example.com/a",
		"http://example.com/b",
		"http://example.com/c", // causes eviction of /a (oldest)
	} {
		key := cache.BuildKey("GET", url, nil)
		e := makeEntry("GET", url, 200, []byte("body"), 60*time.Second, clk)
		c.Put(key, e)
		_ = i
	}

	if c.Len() > 2 {
		t.Errorf("expected at most 2 entries, got %d", c.Len())
	}

	// /a should have been evicted.
	keyA := cache.BuildKey("GET", "http://example.com/a", nil)
	if _, ok := c.Get(keyA); ok {
		t.Error("expected /a to be evicted after MaxEntries exceeded")
	}
	// /b and /c should still be present.
	for _, url := range []string{"http://example.com/b", "http://example.com/c"} {
		if _, ok := c.Get(cache.BuildKey("GET", url, nil)); !ok {
			t.Errorf("expected %s to still be in cache", url)
		}
	}
}

// ─── TestCacheEvictsMaxBytes ──────────────────────────────────────────────────

func TestCacheEvictsMaxBytes(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	// max 50 bytes total, max entry 30 bytes
	c := newTestCache(100, 50, 30, clk)

	// Insert two 20-byte entries (total 40 bytes, within limit).
	for _, url := range []string{"http://example.com/x", "http://example.com/y"} {
		key := cache.BuildKey("GET", url, nil)
		e := makeEntry("GET", url, 200, []byte("12345678901234567890"), 60*time.Second, clk) // 20 bytes
		c.Put(key, e)
	}
	if c.TotalBytes() != 40 {
		t.Fatalf("want 40 total bytes, got %d", c.TotalBytes())
	}

	// Insert a 20-byte entry that would push total to 60 — should evict /x.
	key := cache.BuildKey("GET", "http://example.com/z", nil)
	e := makeEntry("GET", "http://example.com/z", 200, []byte("12345678901234567890"), 60*time.Second, clk)
	c.Put(key, e)

	if c.TotalBytes() > 50 {
		t.Errorf("total bytes %d exceeds MaxBytes 50", c.TotalBytes())
	}
	// /x should have been evicted.
	if _, ok := c.Get(cache.BuildKey("GET", "http://example.com/x", nil)); ok {
		t.Error("expected /x to be evicted to stay within MaxBytes")
	}
}

// ─── TestCacheDefensiveCopy ───────────────────────────────────────────────────

func TestCacheDefensiveCopy(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	c := newTestCache(10, 1024, 512, clk)

	originalBody := []byte("original")
	key := cache.BuildKey("GET", "http://example.com/copy", nil)
	e := makeEntry("GET", "http://example.com/copy", 200, originalBody, 60*time.Second, clk)
	// Store a defensive copy of headers.
	e.Header = cache.CopyHeader(e.Header)
	c.Put(key, e)

	// Mutate the original body — should not affect what's in cache.
	originalBody[0] = 'X'

	got, ok := c.Get(key)
	if !ok {
		t.Fatal("expected cache HIT")
	}
	if got.Body[0] == 'X' {
		t.Error("cache entry was mutated via original body slice (missing defensive copy)")
	}
}

// ─── TestCacheStaleEntryReturnsM iss ─────────────────────────────────────────

func TestCacheStaleEntryReturnsMiss(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	c := newTestCache(10, 1024, 512, clk)

	key := cache.BuildKey("GET", "http://example.com/stale", nil)
	e := makeEntry("GET", "http://example.com/stale", 200, []byte("data"), 1*time.Second, clk)
	c.Put(key, e)

	// Advance past TTL.
	clk.Advance(2 * time.Second)

	if _, ok := c.Get(key); ok {
		t.Error("stale entry must not be returned")
	}
}

// ─── TTLFor tests ─────────────────────────────────────────────────────────────

func TestTTLForMaxAge(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Cache-Control", "max-age=120")
	ttl := cache.TTLFor(hdr, "http://example.com/api", 300, time.Now())
	if ttl != 120*time.Second {
		t.Errorf("want 120s, got %v", ttl)
	}
}

func TestTTLForStaticExtension(t *testing.T) {
	hdr := make(http.Header)
	ttl := cache.TTLFor(hdr, "http://example.com/style.css", 300, time.Now())
	if ttl != 300*time.Second {
		t.Errorf("want 300s for .css, got %v", ttl)
	}
}

func TestTTLForHTMLNoFreshness(t *testing.T) {
	hdr := make(http.Header)
	ttl := cache.TTLFor(hdr, "http://example.com/index.html", 300, time.Now())
	if ttl != 0 {
		t.Errorf("want 0 for .html without freshness header, got %v", ttl)
	}
}

func TestTTLForAPIPathNoFreshness(t *testing.T) {
	hdr := make(http.Header)
	ttl := cache.TTLFor(hdr, "http://example.com/api/data", 300, time.Now())
	if ttl != 0 {
		t.Errorf("want 0 for API path without freshness header, got %v", ttl)
	}
}

// ─── BuildKey includes Accept-Encoding ────────────────────────────────────────

func TestBuildKeyIncludesAcceptEncoding(t *testing.T) {
	hdr1 := http.Header{"Accept-Encoding": []string{"gzip"}}
	hdr2 := http.Header{"Accept-Encoding": []string{"identity"}}
	hdrNone := http.Header{}

	k1 := cache.BuildKey("GET", "http://example.com/", hdr1)
	k2 := cache.BuildKey("GET", "http://example.com/", hdr2)
	kN := cache.BuildKey("GET", "http://example.com/", hdrNone)

	if k1 == k2 {
		t.Error("keys with different Accept-Encoding must differ")
	}
	if k1 == kN {
		t.Error("key with gzip must differ from key without Accept-Encoding")
	}
}

// ─── IsResponseCacheable — Vary ───────────────────────────────────────────────

func TestResponseCacheableVaryAcceptEncoding(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Vary", "Accept-Encoding")
	ok := cache.IsResponseCacheable("GET", 200, hdr, 0, cache.ResponseOptions{MaxEntryBytes: 1024})
	if !ok {
		t.Error("Vary: Accept-Encoding should be cacheable")
	}
}

func TestResponseCacheableVaryUnsupported(t *testing.T) {
	hdr := make(http.Header)
	hdr.Set("Vary", "User-Agent")
	ok := cache.IsResponseCacheable("GET", 200, hdr, 0, cache.ResponseOptions{MaxEntryBytes: 1024})
	if ok {
		t.Error("Vary: User-Agent should not be cacheable (unsupported Vary field)")
	}
}
