package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tiroq/chunkbridge/internal/cache"
	"github.com/tiroq/chunkbridge/internal/config"
	cbcrypto "github.com/tiroq/chunkbridge/internal/crypto"
	"github.com/tiroq/chunkbridge/internal/exit"
	"github.com/tiroq/chunkbridge/internal/proxy"
	"github.com/tiroq/chunkbridge/internal/ratelimit"
	"github.com/tiroq/chunkbridge/internal/transport"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println("chunkbridge", version)

	case "client":
		runClient()

	case "exit":
		runExit()

	case "selftest":
		runSelftest()

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: chunkbridge <command>")
	fmt.Fprintln(os.Stderr, "Commands: client, exit, selftest, version")
}

func runClient() {
	cfgPath := "chunkbridge.client.yaml"
	if len(os.Args) > 2 {
		cfgPath = os.Args[2]
	}
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	key, err := deriveKey(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	t, err := buildTransport(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer t.Close()

	lim := buildRateLimiter(cfg)
	p := proxy.NewHTTPProxy(t, key, *cfg)
	if lim != nil {
		p.WithRateLimiter(lim)
	}
	if cfg.Cache.Enabled {
		c := cache.New(cache.Config{
			MaxEntries:             cfg.Cache.MaxEntries,
			MaxBytes:               cfg.Cache.MaxBytes,
			MaxEntryBytes:          cfg.Cache.MaxEntryBytes,
			DefaultTTLSeconds:      cfg.Cache.DefaultTTLSeconds,
			CachePrivate:           cfg.Cache.CachePrivate,
			CacheWithCookies:       cfg.Cache.CacheWithCookies,
			CacheWithAuthorization: cfg.Cache.CacheWithAuthorization,
		})
		p.WithCache(c)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Listen.Address, cfg.Listen.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen %s: %v\n", addr, err)
		os.Exit(1)
	}
	fmt.Printf("chunkbridge client proxy listening on %s\n", ln.Addr())

	// Trap SIGINT/SIGTERM for graceful shutdown.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- p.Serve(ln) }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "proxy: %v\n", err)
			os.Exit(1)
		}
	case <-sigCtx.Done():
		fmt.Println("chunkbridge client shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "proxy shutdown: %v\n", err)
		}
	}
}

func runExit() {
	cfgPath := "chunkbridge.exit.yaml"
	if len(os.Args) > 2 {
		cfgPath = os.Args[2]
	}
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	key, err := deriveKey(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	t, err := buildTransport(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer t.Close()

	lim := buildRateLimiter(cfg)
	executor := exit.NewHTTPExecutor(t, key, *cfg)
	if lim != nil {
		executor.WithRateLimiter(lim)
	}
	fmt.Println("chunkbridge exit node running")

	// Run until SIGINT/SIGTERM or transport closes.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := executor.Run(sigCtx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "exit: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("chunkbridge exit node stopped")
}

func runSelftest() {
	fmt.Println("=== chunkbridge selftest ===")
	failures := 0

	// 1. Start an in-memory test HTTP server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello":
			w.WriteHeader(200)
			_, _ = w.Write([]byte("hello from exit"))
		case "/echo":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(200)
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write(body)
		case "/big":
			w.WriteHeader(200)
			_, _ = w.Write(bytes.Repeat([]byte("x"), 128*1024))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	// 2. Derive key.
	salt := []byte("selftestsalt1234") // exactly 16 bytes
	key, err := cbcrypto.DeriveKey([]byte("selftestpassword"), salt, cbcrypto.DefaultDeriveParams)
	if err != nil {
		fmt.Printf("FAIL: key derivation: %v\n", err)
		os.Exit(1)
	}

	// 3. Create memory transport pair.
	clientTransport, exitTransport := transport.NewMemoryPair(transport.MemoryOptions{})

	// 4. Start exit executor.
	exitCfg := config.DefaultExitConfig()
	exitCfg.Policy.BlockPrivateRanges = false // allow localhost for selftest
	executor := exit.NewHTTPExecutor(exitTransport, key, exitCfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = executor.Run(ctx) }()

	// 5. Start proxy on a random port.
	proxyCfg := config.DefaultClientConfig()
	p := proxy.NewHTTPProxy(clientTransport, key, proxyCfg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("FAIL: listen: %v\n", err)
		os.Exit(1)
	}
	go func() { _ = p.Serve(ln) }()
	// Wait for the proxy server to start accepting connections.
	if err := waitForListener(ln.Addr().String()); err != nil {
		fmt.Printf("FAIL: proxy did not start: %v\n", err)
		os.Exit(1)
	}

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   30 * time.Second,
	}

	// Test: GET request.
	resp, err := httpClient.Get(ts.URL + "/hello")
	if err != nil {
		fmt.Printf("FAIL: GET /hello: %v\n", err)
		failures++
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 && string(body) == "hello from exit" {
			fmt.Println("PASS: GET /hello")
		} else {
			fmt.Printf("FAIL: GET /hello: status=%d body=%q\n", resp.StatusCode, body)
			failures++
		}
	}

	// Test: POST echo.
	postBody := `{"msg":"test"}`
	resp, err = httpClient.Post(ts.URL+"/echo", "application/json", bytes.NewBufferString(postBody))
	if err != nil {
		fmt.Printf("FAIL: POST /echo: %v\n", err)
		failures++
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 && string(body) == postBody {
			fmt.Println("PASS: POST /echo")
		} else {
			fmt.Printf("FAIL: POST /echo: status=%d body=%q\n", resp.StatusCode, body)
			failures++
		}
	}

	// Test: large response (128 KB).
	resp, err = httpClient.Get(ts.URL + "/big")
	if err != nil {
		fmt.Printf("FAIL: GET /big: %v\n", err)
		failures++
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 && len(body) == 128*1024 {
			fmt.Println("PASS: GET /big (128 KB)")
		} else {
			fmt.Printf("FAIL: GET /big: status=%d bodyLen=%d\n", resp.StatusCode, len(body))
			failures++
		}
	}

	// Test: version command output.
	vBytes, _ := json.Marshal(map[string]string{"version": version})
	_ = vBytes

	if failures == 0 {
		fmt.Println("=== selftest PASSED ===")
	} else {
		fmt.Printf("=== selftest FAILED (%d failures) ===\n", failures)
		os.Exit(1)
	}
}

func deriveKey(cfg *config.Config) ([]byte, error) {
	envName := cfg.Crypto.PassphraseEnv
	if envName == "" {
		envName = "CHUNKBRIDGE_SHARED_KEY"
	}
	passphrase := os.Getenv(envName)
	if passphrase == "" {
		return nil, fmt.Errorf("config: environment variable %s is not set", envName)
	}
	salt := cfg.Crypto.Salt
	if salt == "" {
		return nil, fmt.Errorf("crypto.salt must be set in config")
	}
	saltBytes := []byte(salt)
	params := cbcrypto.DeriveParams{
		Time:    cfg.Crypto.Argon2Time,
		Memory:  cfg.Crypto.Argon2Mem,
		Threads: cfg.Crypto.Argon2Threads,
	}
	return cbcrypto.DeriveKey([]byte(passphrase), saltBytes, params)
}

// waitForListener retries a TCP dial until the listener is accepting or 3 s elapses.
// It returns an error if the listener does not become ready in time.
func waitForListener(addr string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("proxy listener at %s did not start within 3s", addr)
}

// buildRateLimiter creates an AdaptiveRateLimiter from config when the rates are
// positive. Returns nil when all rates are zero (disables throttling).
func buildRateLimiter(cfg *config.Config) *ratelimit.AdaptiveRateLimiter {
	l := cfg.Limits
	if l.GlobalRPS <= 0 || l.DataRPS <= 0 || l.ControlRPS <= 0 || l.Burst <= 0 {
		return nil
	}
	return ratelimit.NewAdaptiveRateLimiter(l.GlobalRPS, l.DataRPS, l.ControlRPS, l.Burst)
}

func buildTransport(cfg *config.Config) (transport.Transport, error) {
	switch cfg.Transport.Type {
	case "max":
		mc := cfg.Transport.Max
		return transport.NewMaxTransport(transport.MaxTransportConfig{
			BaseURL:        mc.BaseURL,
			TokenEnv:       mc.TokenEnv,
			PeerChatID:     mc.PeerChatID,
			FromHandle:     mc.FromHandle,
			PollIntervalMs: mc.PollMs,
			PollTimeoutSec: mc.PollTimeoutSec,
			SafeChars:      cfg.Limits.Message.SafeChars,
			DedupeMaxIDs:   mc.DedupeMaxIDs,
		})
	case "memory":
		// MemoryTransport is an in-process paired transport; it cannot connect
		// two independent processes. Use it via selftest or integration tests only.
		return nil, fmt.Errorf("transport: memory transport is only available for selftest/in-process integration, not for standalone client or exit mode")
	default:
		// Should not reach here if cfg.Validate() was called first.
		return nil, fmt.Errorf("transport: unknown transport type %q", cfg.Transport.Type)
	}
}
