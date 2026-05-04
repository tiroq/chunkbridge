package relay
package relay_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/protocol"
	"github.com/tiroq/chunkbridge/internal/relay"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// sendErrorFrame encodes and sends a FrameERROR for the given requestID via t.
func sendErrorFrame(t *testing.T, tport transport.Transport, key []byte, sessionID, requestID, code, msg string, httpStatus int) {
	t.Helper()
	payload, err := protocol.MarshalErrorPayload(code, httpStatus, msg)
	if err != nil {
		t.Fatalf("MarshalErrorPayload: %v", err)
	}
	f := &protocol.Frame{
		Version:     1,
		Type:        protocol.FrameERROR,
		SessionID:   sessionID,
		RequestID:   requestID,
		TotalChunks: 1,
		ChunkIndex:  0,
		Payload:     payload,
	}
	text, err := protocol.EncodeMessage(f, key)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := tport.Send(ctx, transport.Message{Text: text}); err != nil {
		t.Fatalf("Send error frame: %v", err)
	}
}

// TestRelayErrorFrameDeliveredToPendingRequest verifies that a FrameERROR from
// the exit side is delivered to the waiting SendRequest caller as a *relay.RelayError.
func TestRelayErrorFrameDeliveredToPendingRequest(t *testing.T) {
	key := testKey(t)
	clientT, exitT := transport.NewMemoryPair(transport.MemoryOptions{})
	defer clientT.Close()
	defer exitT.Close()

	const sessionID = "test-error-delivery"
	const reqID = "req-error-1"

	sess := relay.NewSession(sessionID, clientT, key)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sess.Start(ctx) }()

	// Drain the client→exit direction so the send doesn't block, then inject
	// a FrameERROR after a short delay.
	go func() {
		drainCtx, dcancel := context.WithCancel(ctx)
		defer dcancel()
		ch, _ := exitT.Receive(drainCtx)
		// Wait until we see the request arrive, then reply with an error frame.
		select {
		case <-ch:
		case <-drainCtx.Done():
			return
		}
		// Brief delay so SendRequest is blocked in the select.
		time.Sleep(10 * time.Millisecond)
		sendErrorFrame(t, exitT, key, sessionID, reqID, protocol.ErrCodePolicyDenied, "request denied by policy", 403)
	}()

	frame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: sessionID,
		RequestID: reqID,
		Payload:   []byte(`{"method":"GET","url":"http://example.com/"}`),
	}

	_, err := sess.SendRequest(ctx, frame, 3*time.Second)
	if err == nil {
		t.Fatal("expected RelayError, got nil")
	}
	var relErr *relay.RelayError
	if !errors.As(err, &relErr) {
		t.Fatalf("expected *relay.RelayError, got %T: %v", err, err)
	}
	if relErr.Code != protocol.ErrCodePolicyDenied {
		t.Errorf("code: got %q want %q", relErr.Code, protocol.ErrCodePolicyDenied)
	}
	if relErr.HTTPStatus != 403 {
		t.Errorf("http_status: got %d want 403", relErr.HTTPStatus)
	}
	if relErr.Message == "" {
		t.Error("message should not be empty")
	}
}

// TestRelayErrorFrameForMissingRequestIgnored verifies that a FrameERROR that
// arrives for a request ID that is not in the pending map is silently dropped
// and does not panic or block.
func TestRelayErrorFrameForMissingRequestIgnored(t *testing.T) {
	key := testKey(t)
	clientT, exitT := transport.NewMemoryPair(transport.MemoryOptions{})
	defer clientT.Close()
	defer exitT.Close()

	const sessionID = "test-error-orphan"

	sess := relay.NewSession(sessionID, clientT, key)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sess.Start(ctx) }()

	// Give Start a moment to be listening.
	time.Sleep(10 * time.Millisecond)

	// Send a FrameERROR for a request ID that was never registered.
	sendErrorFrame(t, exitT, key, sessionID, "no-such-request", protocol.ErrCodeInternalError, "oops", 502)

	// Give the dispatch goroutine time to process the frame. If it panics, the
	// test process crashes; if it hangs, the test times out.
	time.Sleep(50 * time.Millisecond)
	// Reaching here means the orphan error was handled gracefully.
}

// TestRelayErrorDoesNotLeakSensitiveBody verifies that the RelayError message
// field contains only the safe summary string from sendError, not the original
// request URL, headers, or upstream response body.
func TestRelayErrorDoesNotLeakSensitiveBody(t *testing.T) {
	key := testKey(t)
	clientT, exitT := transport.NewMemoryPair(transport.MemoryOptions{})
	defer clientT.Close()
	defer exitT.Close()

	const sessionID = "test-error-no-leak"
	const reqID = "req-no-leak"

	sess := relay.NewSession(sessionID, clientT, key)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sess.Start(ctx) }()

	go func() {
		drainCtx, dcancel := context.WithCancel(ctx)
		defer dcancel()
		ch, _ := exitT.Receive(drainCtx)
		select {
		case <-ch:
		case <-drainCtx.Done():
			return
		}
		time.Sleep(10 * time.Millisecond)
		// Safe message: no URL, no body, no headers.
		sendErrorFrame(t, exitT, key, sessionID, reqID, protocol.ErrCodeUpstreamUnavailable, "upstream unavailable", 502)
	}()

	frame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: sessionID,
		RequestID: reqID,
		Payload:   []byte(`{"method":"GET","url":"http://secret.internal/api/token?secret=abc123"}`),
	}

	_, err := sess.SendRequest(ctx, frame, 3*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	var relErr *relay.RelayError
	if !errors.As(err, &relErr) {
		t.Fatalf("expected *relay.RelayError, got %T", err)
	}
	// The message must not contain the request URL or any path fragment.
	if contains(relErr.Message, "secret") || contains(relErr.Message, "token") || contains(relErr.Message, "abc123") {
		t.Errorf("RelayError.Message leaks sensitive data: %q", relErr.Message)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
