package integration_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/config"
	cbcrypto "github.com/tiroq/chunkbridge/internal/crypto"
	"github.com/tiroq/chunkbridge/internal/exit"
	"github.com/tiroq/chunkbridge/internal/proxy"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// testKey is a pre-derived 32-byte key used in all integration tests.
// We derive it once at package level to avoid slow Argon2 per-test.
var testKey []byte

func init() {
	var err error
	testKey, err = cbcrypto.DeriveKey([]byte("integrationtest"), []byte("integsalt1234567"), cbcrypto.DefaultDeriveParams)
	if err != nil {
		panic("init: derive key: " + err.Error())
	}
}

// newRelayPair starts a client proxy and an exit executor connected via an
// in-memory transport. It returns the proxy's listener address and a teardown
// function.
func newRelayPair(t *testing.T, exitCfg config.Config) (proxyAddr string, teardown func()) {
	t.Helper()

	clientTransport, exitTransport := transport.NewMemoryPair(transport.MemoryOptions{})

	executor := exit.NewHTTPExecutor(exitTransport, testKey, exitCfg)
	ctx, cancel := context.WithCancel(context.Background())

	go func() { _ = executor.Run(ctx) }()

	proxyCfg := config.DefaultClientConfig()
	p := proxy.NewHTTPProxy(clientTransport, testKey, proxyCfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = p.Serve(ln) }()

	// Wait for the proxy to start accepting connections.
	waitForListener(t, ln.Addr().String())

	return ln.Addr().String(), func() {
		cancel()
		_ = clientTransport.Close()
		_ = exitTransport.Close()
	}
}

func waitForListener(t *testing.T, addr string) {
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
	t.Fatalf("server at %s did not start within 3s", addr)
}

func proxyHTTPClient(proxyAddr string) *http.Client {
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   30 * time.Second,
	}
}

func TestMemoryRelayGET(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("get-ok"))
	}))
	defer ts.Close()

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := newRelayPair(t, exitCfg)
	defer teardown()

	client := proxyHTTPClient(addr)
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d want 200", resp.StatusCode)
	}
	if string(body) != "get-ok" {
		t.Errorf("body: got %q want %q", body, "get-ok")
	}
}

func TestMemoryRelayPOST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := newRelayPair(t, exitCfg)
	defer teardown()

	client := proxyHTTPClient(addr)
	payload := `{"hello":"world"}`
	resp, err := client.Post(ts.URL+"/", "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != payload {
		t.Errorf("echo body: got %q want %q", body, payload)
	}
}

func TestMemoryRelay1MBResponse(t *testing.T) {
	bigBody := bytes.Repeat([]byte("x"), 1024*1024)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bigBody)
	}))
	defer ts.Close()

	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false

	addr, teardown := newRelayPair(t, exitCfg)
	defer teardown()

	client := proxyHTTPClient(addr)
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET 1MB: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if len(body) != len(bigBody) {
		t.Errorf("1MB response: got %d bytes want %d", len(body), len(bigBody))
	}
	if !bytes.Equal(body, bigBody) {
		t.Error("1MB response body mismatch")
	}
}
