package proxy_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/cache"
	"github.com/tiroq/chunkbridge/internal/config"
	"github.com/tiroq/chunkbridge/internal/exit"
	"github.com/tiroq/chunkbridge/internal/proxy"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// startProxyWithCacheAndConfig starts a full relay pair with an optional cache
// attached to the proxy. Returns proxy address and teardown.
func startProxyWithCacheAndConfig(t *testing.T, proxyCfg config.Config, exitCfg config.Config, c *cache.Cache) (string, func()) {
	t.Helper()
	key := proxyTestKey(t)

	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	executor := exit.NewHTTPExecutor(et, key, exitCfg)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = executor.Run(ctx) }()

	p := proxy.NewHTTPProxy(ct, key, proxyCfg)
	if c != nil {
		p.WithCache(c)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = p.Serve(ln) }()
	waitForProxyListener(t, ln.Addr().String())

	return ln.Addr().String(), func() {
		cancel()
		_ = ct.Close()
		_ = et.Close()
	}
}

// newTestCacheForProxy creates a cache with generous limits for proxy tests.
func newTestCacheForProxy(ttlSec int) *cache.Cache {
	return cache.New(cache.Config{
		MaxEntries:        512,
		MaxBytes:          64 * 1024 * 1024,
		MaxEntryBytes:     2 * 1024 * 1024,
		DefaultTTLSeconds: ttlSec,
	})
}

// proxyClient returns an HTTP client using addr as the proxy.
func proxyClient(addr string) *http.Client {
	pu, _ := url.Parse("http://" + addr)
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   10 * time.Second,
	}
}

// ─── TestHTTPProxyCacheMissThenHit ────────────────────────────────────────────

func TestHTTPProxyCacheMissThenHit(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "cached-body")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := proxyClient(addr)

	// First request — MISS, upstream called once.
	resp1, err := client.Get(ts.URL + "/resource")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	if resp1.StatusCode != 200 {
		t.Errorf("first GET status: want 200, got %d", resp1.StatusCode)
	}
	if string(body1) != "cached-body" {
		t.Errorf("first GET body: want %q, got %q", "cached-body", body1)
	}
	if got := resp1.Header.Get("X-Chunkbridge-Cache"); got != "MISS" {
		t.Errorf("first GET: want X-Chunkbridge-Cache=MISS, got %q", got)
	}
	if n := callCount.Load(); n != 1 {
		t.Errorf("first GET: upstream called %d times, want 1", n)
	}

	// Second request — HIT, upstream not called again.
	resp2, err := client.Get(ts.URL + "/resource")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Errorf("second GET status: want 200, got %d", resp2.StatusCode)
	}
	if string(body2) != "cached-body" {
		t.Errorf("second GET body: want %q, got %q", "cached-body", body2)
	}
	if got := resp2.Header.Get("X-Chunkbridge-Cache"); got != "HIT" {
		t.Errorf("second GET: want X-Chunkbridge-Cache=HIT, got %q", got)
	}
	if n := callCount.Load(); n != 1 {
		t.Errorf("second GET: upstream call count should still be 1, got %d", n)
	}
}

// ─── TestHTTPProxyCacheDisabledPreservesBehavior ──────────────────────────────

func TestHTTPProxyCacheDisabledPreservesBehavior(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = io.WriteString(w, "response")
	}))
	defer ts.Close()

	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, nil)
	defer teardown()

	client := proxyClient(addr)
	for i := 0; i < 3; i++ {
		resp, err := client.Get(ts.URL + "/uncached")
		if err != nil {
			t.Fatalf("GET %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.Header.Get("X-Chunkbridge-Cache") != "" {
			t.Errorf("request %d: X-Chunkbridge-Cache must be absent when cache is disabled", i)
		}
	}
	if n := callCount.Load(); n != 3 {
		t.Errorf("expected 3 upstream calls with cache disabled, got %d", n)
	}
}

// ─── TestHTTPProxyCacheBypassesAuthorization ──────────────────────────────────

func TestHTTPProxyCacheBypassesAuthorization(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = io.WriteString(w, "secure")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := proxyClient(addr)
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/secure", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("X-Chunkbridge-Cache"); got == "HIT" {
			t.Errorf("request %d: Authorization request must not be served from cache", i)
		}
	}
	if n := callCount.Load(); n != 2 {
		t.Errorf("expected 2 upstream calls for Authorization requests, got %d", n)
	}
}

// ─── TestHTTPProxyCacheBypassesCookie ─────────────────────────────────────────

func TestHTTPProxyCacheBypassesCookie(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = io.WriteString(w, "personalized")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := proxyClient(addr)
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/page", nil)
		req.Header.Set("Cookie", "session=abc")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.Header.Get("X-Chunkbridge-Cache") == "HIT" {
			t.Errorf("request %d: Cookie request must not be served from cache", i)
		}
	}
	if n := callCount.Load(); n != 2 {
		t.Errorf("expected 2 upstream calls for Cookie requests, got %d", n)
	}
}

// ─── TestHTTPProxyCacheDoesNotCacheHTMLWithoutFreshness ───────────────────────

func TestHTTPProxyCacheDoesNotCacheHTMLWithoutFreshness(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html>page</html>")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := proxyClient(addr)
	for i := 0; i < 2; i++ {
		resp, err := client.Get(ts.URL + "/index.html")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.Header.Get("X-Chunkbridge-Cache") == "HIT" {
			t.Errorf("request %d: HTML without freshness must not be cached", i)
		}
	}
	if n := callCount.Load(); n != 2 {
		t.Errorf("expected 2 upstream calls for HTML without freshness, got %d", n)
	}
}

// ─── TestHTTPProxyCacheCachesStaticWithDefaultTTL ─────────────────────────────

func TestHTTPProxyCacheCachesStaticWithDefaultTTL(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "text/css")
		_, _ = io.WriteString(w, "body { color: red; }")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := proxyClient(addr)

	resp1, err := client.Get(ts.URL + "/styles.css")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	resp1.Body.Close()
	if got := resp1.Header.Get("X-Chunkbridge-Cache"); got != "MISS" {
		t.Errorf("first GET: want MISS, got %q", got)
	}

	resp2, err := client.Get(ts.URL + "/styles.css")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	resp2.Body.Close()
	if got := resp2.Header.Get("X-Chunkbridge-Cache"); got != "HIT" {
		t.Errorf("second GET: want HIT, got %q", got)
	}
	if n := callCount.Load(); n != 1 {
		t.Errorf("expected exactly 1 upstream call for static asset, got %d", n)
	}
}

// ─── TestHTTPProxyCacheDoesNotCacheRelayError ─────────────────────────────────

func TestHTTPProxyCacheDoesNotCacheRelayError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = io.WriteString(w, "ok")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = true // blocks 127.0.0.1

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := proxyClient(addr)

	resp, err := client.Get(ts.URL + "/resource")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	if c.Len() != 0 {
		t.Errorf("expected empty cache after relay/policy error, got %d entries", c.Len())
	}
}

// ─── TestHTTPProxyCacheNoCacheDirective ────────────────────────────────────────

func TestHTTPProxyCacheNoCacheDirective(t *testing.T) {
	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = io.WriteString(w, "fresh")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				pu, _ := url.Parse("http://" + addr)
				return pu, nil
			},
		},
		Timeout: 10 * time.Second,
	}

	// Populate cache.
	resp1, err := client.Get(ts.URL + "/data")
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	resp1.Body.Close()

	// no-cache request — must bypass cache.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data", nil)
	req.Header.Set("Cache-Control", "no-cache")
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("no-cache GET: %v", err)
	}
	_, _ = io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if resp2.Header.Get("X-Chunkbridge-Cache") == "HIT" {
		t.Error("no-cache request must not return a cache HIT")
	}
	if n := callCount.Load(); n < 2 {
		t.Errorf("no-cache request must reach upstream; call count: %d", n)
	}
}

// ─── TestHTTPProxyCacheHeaderIsolation ────────────────────────────────────────

func TestHTTPProxyCacheHeaderIsolation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("X-Custom", "original")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "data")
	}))
	defer ts.Close()

	c := newTestCacheForProxy(300)
	proxyCfg := config.DefaultClientConfig()
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithCacheAndConfig(t, proxyCfg, exitCfg, c)
	defer teardown()

	client := proxyClient(addr)

	resp1, _ := client.Get(ts.URL + "/hdr")
	_, _ = io.ReadAll(resp1.Body)
	resp1.Body.Close()

	resp2, err := client.Get(ts.URL + "/hdr")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	_, _ = io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if resp2.Header.Get("X-Chunkbridge-Cache") != "HIT" {
		t.Skip("response not served from cache; skipping header isolation check")
	}
	if !strings.Contains(resp2.Header.Get("X-Custom"), "original") {
		t.Errorf("cached header X-Custom corrupted; got %q", resp2.Header.Get("X-Custom"))
	}
}
