package exit
package exit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/config"
	cbcrypto "github.com/tiroq/chunkbridge/internal/crypto"
	"github.com/tiroq/chunkbridge/internal/exit"
	"github.com/tiroq/chunkbridge/internal/protocol"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// testKey derives a deterministic test key.
func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := cbcrypto.DeriveKey(
		[]byte("testpassword"),
		[]byte("selftestsalt1234"),
		cbcrypto.DefaultDeriveParams,
	)
	if err != nil {
		t.Fatalf("testKey: %v", err)
	}
	return key
}

// relayRequest and relayResponse mirror the internal structs in package exit.
type relayRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body,omitempty"`
}

type relayResponse struct {
	StatusCode int                 `json:"status"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body,omitempty"`
}

// sendRelayRequest encodes a relayRequest into the transport, waits for the
// executor to process it, and decodes the relayResponse.
func sendRelayRequest(
	t *testing.T,
	key []byte,
	clientTransport *transport.MemoryTransport,
	req relayRequest,
) relayResponse {
	t.Helper()

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	frame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: "test-session",
		RequestID: "req-0001",
		Payload:   payload,
	}
	// Chunk and send.
	for _, chunk := range protocol.Chunk(*frame, protocol.MaxPayloadBytes) {
		c := chunk
		c.SeqNum = 1
		text, err := protocol.EncodeMessage(&c, key)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := clientTransport.Send(ctx, transport.Message{Text: text}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	// Receive response.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msgCh, err := clientTransport.Receive(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	var responseFrame *protocol.Frame
	reassembler := protocol.NewReassembler(10 * time.Second)
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatal("channel closed before response")
			}
			decoded, err := protocol.DecodeMessage(msg.Text, key)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			complete, ok := reassembler.Add(decoded)
			if ok {
				responseFrame = complete
				goto done
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for response")
		}
	}
done:
	var resp relayResponse
	if err := json.Unmarshal(responseFrame.Payload, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

// startExecutor creates a memory transport pair and starts an HTTPExecutor.
// Returns the client-side transport and a cancel func.
func startExecutor(t *testing.T, key []byte, cfg config.Config) (*transport.MemoryTransport, context.CancelFunc) {
	t.Helper()
	clientT, exitT := transport.NewMemoryPair(transport.MemoryOptions{})
	executor := exit.NewHTTPExecutor(exitT, key, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = executor.Run(ctx) }()
	// Let the executor's goroutine start.
	time.Sleep(10 * time.Millisecond)
	return clientT, cancel
}

// --- Hop-by-hop header tests ---

func TestExitStripsHopByHopHeaders(t *testing.T) {
	var capturedHeaders http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer ts.Close()

	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = false // allow httptest localhost

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	hopHeaders := map[string][]string{
		"Connection":          {"keep-alive"},
		"Keep-Alive":          {"timeout=5"},
		"Transfer-Encoding":   {"chunked"},
		"Upgrade":             {"websocket"},
		"TE":                  {"trailers"},
		"Proxy-Authorization": {"Basic abc123"},
		"Proxy-Connection":    {"keep-alive"},
	}
	// Also include a preserved end-to-end header.
	hopHeaders["Authorization"] = []string{"Bearer mytoken"}
	hopHeaders["Cookie"] = []string{"session=abc"}

	resp := sendRelayRequest(t, key, clientT, relayRequest{
		Method:  "GET",
		URL:     ts.URL + "/",
		Headers: hopHeaders,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	hopByHop := []string{
		"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade",
		"TE", "Proxy-Authorization", "Proxy-Connection",
	}
	for _, h := range hopByHop {
		if capturedHeaders.Get(h) != "" {
			t.Errorf("hop-by-hop header %q was forwarded upstream but should have been stripped", h)
		}
	}
}

func TestExitStripsConnectionListedHeaders(t *testing.T) {
	var capturedHeaders http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer ts.Close()

	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = false

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	resp := sendRelayRequest(t, key, clientT, relayRequest{
		Method: "GET",
		URL:    ts.URL + "/",
		Headers: map[string][]string{
			// Connection header lists a custom hop-by-hop header.
			"Connection":    {"keep-alive, X-Custom-Hop"},
			"X-Custom-Hop":  {"some-value"},
			"Authorization": {"Bearer safe"},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if capturedHeaders.Get("X-Custom-Hop") != "" {
		t.Error("expected X-Custom-Hop listed in Connection to be stripped")
	}
	if capturedHeaders.Get("Connection") != "" {
		t.Error("expected Connection header to be stripped")
	}
}

func TestExitPreservesEndToEndHeaders(t *testing.T) {
	var capturedHeaders http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer ts.Close()

	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = false

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	resp := sendRelayRequest(t, key, clientT, relayRequest{
		Method: "GET",
		URL:    ts.URL + "/",
		Headers: map[string][]string{
			"Authorization": {"Bearer mytoken"},
			"Cookie":        {"session=abc"},
			"User-Agent":    {"chunkbridge-test/1.0"},
			"Accept":        {"application/json"},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	endToEnd := []struct{ header, want string }{
		{"Authorization", "Bearer mytoken"},
		{"Cookie", "session=abc"},
		{"User-Agent", "chunkbridge-test/1.0"},
		{"Accept", "application/json"},
	}
	for _, tc := range endToEnd {
		if got := capturedHeaders.Get(tc.header); got != tc.want {
			t.Errorf("end-to-end header %q: want %q, got %q", tc.header, tc.want, got)
		}
	}
}

// --- DNS rebinding test (uses custom safe dialer via config) ---

// TestExitBlocksDNSRebindingViaPolicy verifies that a request whose URL
// hostname is on the allow-list but literally resolves to a blocked address
// is rejected at the dial phase.  We test this at the policy/dialer layer
// (see dialer_test.go); here we confirm the executor also wires it correctly
// by using a real listener bound to 127.0.0.1 and enabling BlockPrivateRanges.
func TestExitBlocksPrivateTargetWhenEnabled(t *testing.T) {
	// Start a real HTTP server on 127.0.0.1 (loopback).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "should not reach")
	}))
	defer ts.Close()

	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = true // ENABLED — must block loopback

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	// The URL uses a literal 127.0.0.1 IP, which CheckRequest should block.
	// (belt-and-suspenders: both CheckRequest and the dialer will reject it.)
	resp := sendRelayRequest(t, key, clientT, relayRequest{
		Method: "GET",
		URL:    ts.URL + "/",
	})
	if resp.StatusCode == 200 {
		t.Error("expected request to loopback to be blocked, but got 200")
	}
}

// --- helper: avoid importing strings in test for containsString ---

func containsString(s, sub string) bool {
	return strings.Contains(s, sub)
}

// resolverDialTest exercises the dialer layer of the exit executor via a fake
// HTTP server on a dynamically allocated port, checking that private-IP policy
// is applied by the executor's http.Client.
func resolverDialTest(t *testing.T) {
	t.Helper()
	// Bind an echo server on a random port; use its IP in the URL.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	// Parse the loopback address the server is listening on.
	_, port, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = true

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	url := fmt.Sprintf("http://127.0.0.1:%s/", port)
	resp := sendRelayRequest(t, key, clientT, relayRequest{
		Method: "GET",
		URL:    url,
	})
	// Must be blocked — 403 from policy or 502 from dial failure.
	if resp.StatusCode == 200 {
		t.Errorf("expected private target to be blocked, got 200")
	}
}

func TestExitPrivateLiteralIPBlocked(t *testing.T) {
	resolverDialTest(t)
}

// Ensure the test helper compiles even if containsString is not called elsewhere.
var _ = containsString
var _ = bytes.NewReader
