package relay_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	cbcrypto "github.com/tiroq/chunkbridge/internal/crypto"
	"github.com/tiroq/chunkbridge/internal/protocol"
	"github.com/tiroq/chunkbridge/internal/relay"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// testKey derives a deterministic key for session tests.
func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := cbcrypto.DeriveKey(
		[]byte("relaytestpassword"),
		[]byte("relaytest1234567"),
		cbcrypto.DefaultDeriveParams,
	)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	return key
}

// countLimiter allows exactly n tokens and then blocks forever.
type countLimiter struct {
	remaining int
}

func (c *countLimiter) AllowData() bool {
	if c.remaining > 0 {
		c.remaining--
		return true
	}
	return false
}

// tickLimiter emits one token per tick interval.
type tickLimiter struct {
	interval time.Duration
	last     time.Time
}

func (l *tickLimiter) AllowData() bool {
	now := time.Now()
	if l.last.IsZero() || now.Sub(l.last) >= l.interval {
		l.last = now
		return true
	}
	return false
}

// alwaysLimiter always grants tokens.
type alwaysLimiter struct{}

func (alwaysLimiter) AllowData() bool { return true }

// neverLimiter never grants tokens.
type neverLimiter struct{}

func (neverLimiter) AllowData() bool { return false }

func makeTestFrame(requestID string) *protocol.Frame {
	return &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: "test-session",
		RequestID: requestID,
		Payload:   []byte(`{"test":true}`),
	}
}

// TestSessionSendFrameUsesRateLimiter verifies that a slow limiter causes sends
// to take measurably longer than sends with no limiter.
func TestSessionSendFrameUsesRateLimiter(t *testing.T) {
	key := testKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	defer ct.Close()
	defer et.Close()

	// Use a tick limiter that allows one token per 20 ms. Sending 5 chunks
	// must take at least 4 * 20 ms = 80 ms.
	lim := &tickLimiter{interval: 20 * time.Millisecond}

	sess := relay.NewSession("test-session", ct, key)
	sess.WithRateLimiter(lim)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Drain the exit side so Transport.Send never blocks.
	go func() {
		ch, _ := et.Receive(ctx)
		for range ch {
		}
	}()

	// Register a fake response so SendRequest can return.
	// We register a goroutine that injects a fake response after a brief delay.
	// Actually we'll just call sendFrame via SendResponse (no response expected).
	// Build a small payload large enough to produce >1 chunk.
	const numFrames = 5
	start := time.Now()
	for i := range numFrames {
		frame := makeTestFrame(fmt.Sprintf("req-%d", i))
		if err := sess.SendResponse(ctx, frame); err != nil {
			t.Fatalf("SendResponse %d: %v", i, err)
		}
	}
	elapsed := time.Since(start)

	// We sent 5 frames with 1 chunk each; each chunk requires a token.
	// Token 1 is free (first call to AllowData returns true immediately).
	// The remaining 4 tokens require at least one 20 ms interval each.
	// Allow generous margin: require at least 60 ms total.
	const minElapsed = 60 * time.Millisecond
	if elapsed < minElapsed {
		t.Errorf("expected elapsed >= %v with rate limiter; got %v", minElapsed, elapsed)
	}
}

// TestSessionRateLimiterContextCancel verifies that context cancellation while
// waiting for a token causes sendFrame to return the context error promptly.
func TestSessionRateLimiterContextCancel(t *testing.T) {
	key := testKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	defer ct.Close()
	defer et.Close()

	// neverLimiter always returns false — no token will ever be granted.
	sess := relay.NewSession("test-cancel", ct, key)
	sess.WithRateLimiter(neverLimiter{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Drain exit side.
	go func() {
		ch, _ := et.Receive(ctx)
		for range ch {
		}
	}()

	frame := makeTestFrame("req-cancel")
	err := sess.SendResponse(ctx, frame)
	if err == nil {
		t.Fatal("expected error from context cancellation; got nil")
	}
}

// TestSessionNilLimiterUnchanged verifies that a nil limiter preserves existing
// send behaviour with no observable delay.
func TestSessionNilLimiterUnchanged(t *testing.T) {
	key := testKey(t)
	ct, et := transport.NewMemoryPair(transport.MemoryOptions{})
	defer ct.Close()
	defer et.Close()

	sess := relay.NewSession("test-nil-lim", ct, key)
	// No limiter set.

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		ch, _ := et.Receive(ctx)
		for range ch {
		}
	}()

	for i := range 10 {
		frame := makeTestFrame(fmt.Sprintf("req-nil-%d", i))
		if err := sess.SendResponse(ctx, frame); err != nil {
			t.Fatalf("SendResponse %d: %v", i, err)
		}
	}
}
