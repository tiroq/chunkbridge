package exit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/config"
	"github.com/tiroq/relaykit/pkg/protocol"
	"github.com/tiroq/relaykit/pkg/transport"
)

// receiveFrame sends a relay request to the executor and waits for the first
// reassembled response frame. Unlike sendRelayRequest, it returns the raw
// frame so the caller can inspect its Type field.
func receiveFrame(
	t *testing.T,
	key []byte,
	clientT *transport.MemoryTransport,
	reqID string,
	payload []byte,
) *protocol.Frame {
	t.Helper()

	frame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: "test-session",
		RequestID: reqID,
		Payload:   payload,
	}
	seqNum := uint32(1)
	for _, chunk := range protocol.Chunk(*frame, protocol.MaxPayloadBytes) {
		c := chunk
		c.SeqNum = seqNum
		seqNum++
		text, err := protocol.EncodeMessage(&c, key)
		if err != nil {
			t.Fatalf("receiveFrame: encode: %v", err)
		}
		sendCtx, sendCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sendCancel()
		if err := clientT.Send(sendCtx, transport.Message{Text: text}); err != nil {
			t.Fatalf("receiveFrame: send: %v", err)
		}
	}

	// Receive and reassemble the response.
	recvCtx, recvCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer recvCancel()
	msgCh, err := clientT.Receive(recvCtx)
	if err != nil {
		t.Fatalf("receiveFrame: receive: %v", err)
	}

	reassembler := protocol.NewReassembler(10 * time.Second)
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatal("receiveFrame: channel closed before response")
			}
			decoded, err := protocol.DecodeMessage(msg.Text, key)
			if err != nil {
				t.Fatalf("receiveFrame: decode: %v", err)
			}
			if complete, ok := reassembler.Add(decoded); ok {
				return complete
			}
		case <-recvCtx.Done():
			t.Fatal("receiveFrame: timeout waiting for response")
		}
	}
}

// assertErrorFrame checks that the frame is a FrameERROR with the expected
// error code and http status, and returns the ErrorPayload.
func assertErrorFrame(t *testing.T, f *protocol.Frame, wantCode string, wantHTTPStatus int) protocol.ErrorPayload {
	t.Helper()
	if f.Type != protocol.FrameERROR {
		t.Errorf("frame type: got %d want %d (FrameERROR)", f.Type, protocol.FrameERROR)
	}
	ep, err := protocol.UnmarshalErrorPayload(f.Payload)
	if err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if ep.Code != wantCode {
		t.Errorf("error code: got %q want %q", ep.Code, wantCode)
	}
	if ep.HTTPStatus != wantHTTPStatus {
		t.Errorf("http_status: got %d want %d", ep.HTTPStatus, wantHTTPStatus)
	}
	return ep
}

// TestExitPolicyDenialSendsErrorFrame verifies that a request targeting a
// private IP (with BlockPrivateRanges=true) yields a FrameERROR with
// code=policy_denied and http_status=403.
func TestExitPolicyDenialSendsErrorFrame(t *testing.T) {
	// Bind a real server on loopback; its address will be blocked by policy.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer ts.Close()

	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = true

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	payload, _ := json.Marshal(relayRequest{Method: "GET", URL: ts.URL + "/"})
	f := receiveFrame(t, key, clientT, "req-policy-denied", payload)
	assertErrorFrame(t, f, protocol.ErrCodePolicyDenied, http.StatusForbidden)
}

// TestExitMalformedRequestSendsBadRequestError verifies that a frame whose
// payload is not valid JSON yields a FrameERROR with code=bad_request.
func TestExitMalformedRequestSendsBadRequestError(t *testing.T) {
	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = false

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	f := receiveFrame(t, key, clientT, "req-malformed", []byte("not-valid-json{{"))
	assertErrorFrame(t, f, protocol.ErrCodeBadRequest, http.StatusBadRequest)
}

// TestExitResponseTooLargeSendsErrorFrame verifies that when the upstream
// response exceeds MaxResponseBytes, a FrameERROR with code=response_too_large
// is returned to the proxy.
func TestExitResponseTooLargeSendsErrorFrame(t *testing.T) {
	const maxBytes = 512
	// Upstream returns maxBytes+1 bytes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write(make([]byte, maxBytes+1))
	}))
	defer ts.Close()

	key := testKey(t)
	cfg := config.DefaultExitConfig()
	cfg.Policy.BlockPrivateRanges = false
	cfg.Policy.MaxResponseBytes = maxBytes

	clientT, cancel := startExecutor(t, key, cfg)
	defer cancel()

	payload, _ := json.Marshal(relayRequest{Method: "GET", URL: ts.URL + "/"})
	f := receiveFrame(t, key, clientT, "req-too-large", payload)
	assertErrorFrame(t, f, protocol.ErrCodeResponseTooLarge, http.StatusBadGateway)
}
