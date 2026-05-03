package relay_test

import (
	"context"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/protocol"
	"github.com/tiroq/chunkbridge/internal/relay"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// makeFrame is a small helper shared within this test file.
func makeFrame(requestID string) *protocol.Frame {
	return &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: "test-session",
		RequestID: requestID,
		Payload:   []byte(`{"x":1}`),
	}
}

// TestSessionMaxPendingRequestsRejectsOverflow verifies that when maxPending=1
// and one request is already in-flight, a second concurrent SendRequest is
// rejected immediately with "relay: too many concurrent requests".
func TestSessionMaxPendingRequestsRejectsOverflow(t *testing.T) {
	key := testKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	defer ct.Close()
	defer et.Close()

	sess := relay.NewSession("sess-overflow", ct, key)
	sess.WithMaxPendingRequests(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the session dispatcher.
	go func() { _ = sess.Start(ctx) }()

	// Hold the exit side — never read from it so the first request blocks
	// waiting for a response.
	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		// Signal that we're about to call SendRequest.
		close(firstStarted)
		_, err := sess.SendRequest(ctx, makeFrame("req-1"), 5*time.Second)
		firstDone <- err
	}()

	<-firstStarted
	// Give the first goroutine a moment to register into pending.
	time.Sleep(20 * time.Millisecond)

	// Second request must be rejected immediately.
	_, err := sess.SendRequest(ctx, makeFrame("req-2"), 5*time.Second)
	if err == nil {
		t.Fatal("expected error for second request beyond max pending, got nil")
	}
	if err.Error() != "relay: too many concurrent requests" {
		t.Errorf("unexpected error: %v", err)
	}

	// Cancel so the first request also unblocks.
	cancel()
	<-firstDone
}

// TestSessionPendingCleanedAfterTimeout verifies that after a request times out,
// its pending entry is removed, and a subsequent request with maxPending=1 can
// enter the pending map rather than being rejected.
func TestSessionPendingCleanedAfterTimeout(t *testing.T) {
	key := testKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	defer ct.Close()
	defer et.Close()

	sess := relay.NewSession("sess-timeout-cleanup", ct, key)
	sess.WithMaxPendingRequests(1)

	ctx := context.Background()

	// Start the session dispatcher.
	go func() { _ = sess.Start(ctx) }()

	// Do not drain et, so the send will be buffered but no response comes back.
	// The first request should time out after a short duration.
	_, err := sess.SendRequest(ctx, makeFrame("req-timeout"), 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Drain the send side so the second request's send does not block.
	drainCtx, drainCancel := context.WithCancel(ctx)
	defer drainCancel()
	go func() {
		ch, _ := et.Receive(drainCtx)
		for range ch {
		}
	}()

	// Second request must not be rejected — the first's entry was cleaned up.
	// It will time out too, but importantly it should NOT return "too many concurrent".
	_, err = sess.SendRequest(ctx, makeFrame("req-after-timeout"), 50*time.Millisecond)
	if err != nil && err.Error() == "relay: too many concurrent requests" {
		t.Error("pending entry leaked after timeout: second request was rejected")
	}
}

// TestSessionLateResponseAfterCleanupDoesNotPanic verifies that dispatch()
// handles a missing pending entry gracefully when a response arrives after the
// request has already been cleaned up (timed out / cancelled).
func TestSessionLateResponseAfterCleanupDoesNotPanic(t *testing.T) {
	key := testKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	defer ct.Close()
	defer et.Close()

	sess := relay.NewSession("sess-late-resp", ct, key)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sess.Start(ctx) }()

	const reqID = "req-late"

	// Request times out before the exit side sends anything.
	_, err := sess.SendRequest(ctx, makeFrame(reqID), 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}

	// Now synthesise a late response frame from the exit side. If dispatch()
	// panics on missing pending entry the test will fail.
	respFrame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: "sess-late-resp",
		RequestID: reqID,
		Payload:   []byte(`{"status":200}`),
	}
	// Encode and send via the exit transport so Start() picks it up.
	text, encErr := protocol.EncodeMessage(respFrame, key)
	if encErr != nil {
		t.Fatalf("encode late response: %v", encErr)
	}
	// Drain the client->exit direction first so the memory transport is not stuck.
	drainCtx, drainCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer drainCancel()
	go func() {
		ch, _ := et.Receive(drainCtx)
		for range ch {
		}
	}()

	sendCtx, sendCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer sendCancel()
	// Send the late response from exit->client direction.
	_ = et.Send(sendCtx, transport.Message{Text: text})

	// Give Start() time to process it.
	time.Sleep(30 * time.Millisecond)
	// If we reach here without panic, the test passes.
}
