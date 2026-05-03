package proxy_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/config"
	cbcrypto "github.com/tiroq/chunkbridge/internal/crypto"
	"github.com/tiroq/chunkbridge/internal/exit"
	"github.com/tiroq/chunkbridge/internal/proxy"
	"github.com/tiroq/chunkbridge/internal/transport"
)

func proxyTestKey(t *testing.T) []byte {
	t.Helper()
	key, err := cbcrypto.DeriveKey(
		[]byte("proxytestpassword"),
		[]byte("proxytest1234567"),
		cbcrypto.DefaultDeriveParams,
	)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	return key
}

func waitForProxyListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("proxy at %s did not start within 3s", addr)
}

// startProxyWithConfig starts an HTTP proxy backed by a memory transport pair.
// The exit executor honours exitCfg. Returns the proxy address and a teardown func.
func startProxyWithConfig(t *testing.T, proxyCfg config.Config, exitCfg config.Config) (string, func()) {
	t.Helper()
	key := proxyTestKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})

	executor := exit.NewHTTPExecutor(et, key, exitCfg)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = executor.Run(ctx) }()

	p := proxy.NewHTTPProxy(ct, key, proxyCfg)
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

// TestHTTPProxyConcurrentLimitReturns429 verifies that when max_concurrent_requests=1
// and one request is held in-flight, a second request is rejected with HTTP 429.
func TestHTTPProxyConcurrentLimitReturns429(t *testing.T) {
	// Upstream holds requests until signalled to release.
	release := make(chan struct{})
	firstArrived := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal the first request has arrived at the upstream.
		select {
		case <-firstArrived:
		default:
			close(firstArrived)
		}
		// Block until the test releases us.
		<-release
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "ok")
	}))
	defer ts.Close()

	proxyCfg := config.DefaultClientConfig()
	proxyCfg.Proxy.MaxConcurrentRequests = 1
	proxyCfg.Proxy.RequestTimeoutMs = 5000

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithConfig(t, proxyCfg, exitCfg)
	defer teardown()

	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}

	// Issue the first request asynchronously; it will block at the upstream.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := client.Get(ts.URL + "/block")
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Wait for the first request to reach the upstream server.
	select {
	case <-firstArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach upstream in time")
	}

	// Issue the second request — it must be rejected with 429.
	resp, err := client.Get(ts.URL + "/second")
	if err != nil {
		t.Fatalf("second request: unexpected error %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 429, got %d (body: %q)", resp.StatusCode, body)
	}

	// Release the blocked first request and wait for it to complete.
	close(release)
	wg.Wait()
}

// TestHTTPProxyRequestTimeoutReturns504 verifies that when the exit side never
// responds, the proxy returns a 502 (relay error) after the configured timeout.
// We use 502 because the current proxy maps relay timeout -> 502 Bad Gateway.
func TestHTTPProxyRequestTimeoutReturns502(t *testing.T) {
	// Upstream never responds.
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer ts.Close()
	defer close(block)

	proxyCfg := config.DefaultClientConfig()
	proxyCfg.Proxy.RequestTimeoutMs = 80 // very short: 80 ms

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithConfig(t, proxyCfg, exitCfg)
	defer teardown()

	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	start := time.Now()
	resp, err := client.Get(ts.URL + "/slow")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected connection error: %v", err)
	}
	defer resp.Body.Close()

	// Must be 502 (relay timeout).
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}
	// Must return before the outer 5 s client timeout.
	if elapsed > 3*time.Second {
		t.Errorf("expected timeout within ~80 ms, took %v", elapsed)
	}
}
