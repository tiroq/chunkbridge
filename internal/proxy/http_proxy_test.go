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

// TestHTTPProxyMapsPolicyErrorTo403 verifies that when the exit node rejects a
// request due to policy (private IP blocked), the proxy returns HTTP 403.
func TestHTTPProxyMapsPolicyErrorTo403(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	proxyCfg := config.DefaultClientConfig()
	// Disable client-side policy check so the request reaches the exit.
	proxyCfg.Policy.BlockPrivateRanges = false

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = true // exit blocks private IPs

	addr, teardown := startProxyWithConfig(t, proxyCfg, exitCfg)
	defer teardown()

	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 403, got %d (body: %q)", resp.StatusCode, body)
	}
}

// TestHTTPProxyMapsUpstreamUnavailableTo502 verifies that when the exit cannot
// connect to the upstream server, the proxy returns HTTP 502.
func TestHTTPProxyMapsUpstreamUnavailableTo502(t *testing.T) {
	// Start a server and immediately close it so connections are refused.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unavailableURL := ts.URL
	ts.Close() // close immediately — subsequent connections will be refused

	proxyCfg := config.DefaultClientConfig()
	proxyCfg.Policy.BlockPrivateRanges = false

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := startProxyWithConfig(t, proxyCfg, exitCfg)
	defer teardown()

	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}

	resp, err := client.Get(unavailableURL + "/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 502, got %d (body: %q)", resp.StatusCode, body)
	}
}

// TestHTTPProxyMapsUpstreamTimeoutTo504 verifies that when the upstream server
// does not respond within the exit's request timeout, the proxy returns HTTP 504.
func TestHTTPProxyMapsUpstreamTimeoutTo504(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer ts.Close()
	defer close(block)

	proxyCfg := config.DefaultClientConfig()
	proxyCfg.Policy.BlockPrivateRanges = false
	// Give the proxy plenty of time so it waits for the exit error frame.
	proxyCfg.Proxy.RequestTimeoutMs = 5000

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false
	exitCfg.Exit.RequestTimeoutSec = 1 // exit times out after 1 s

	addr, teardown := startProxyWithConfig(t, proxyCfg, exitCfg)
	defer teardown()

	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}

	resp, err := client.Get(ts.URL + "/slow")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusGatewayTimeout {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 504, got %d (body: %q)", resp.StatusCode, body)
	}
}

// TestHTTPProxyShutdownStopsServer verifies that Shutdown causes the proxy to
// stop accepting new connections and that p.Serve returns http.ErrServerClosed.
func TestHTTPProxyShutdownStopsServer(t *testing.T) {
	key := proxyTestKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	defer ct.Close()
	defer et.Close()

	cfg := config.DefaultClientConfig()
	p := proxy.NewHTTPProxy(ct, key, cfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	serveErr := make(chan error, 1)
	go func() { serveErr <- p.Serve(ln) }()
	waitForProxyListener(t, addr)

	// Trigger graceful shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-serveErr:
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("Serve: unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Serve did not return after Shutdown")
	}

	// New connections should be refused.
	conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		t.Error("expected connection refused after shutdown, but dial succeeded")
	}
}
