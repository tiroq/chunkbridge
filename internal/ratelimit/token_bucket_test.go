package ratelimit_test

import (
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/ratelimit"
)

func TestBurstAllowsUpToBurst(t *testing.T) {
	tb := ratelimit.NewTokenBucket(1, 5) // 1 RPS, burst=5

	for i := 0; i < 5; i++ {
		if !tb.Allow() {
			t.Fatalf("expected Allow()=true on call %d", i+1)
		}
	}
	if tb.Allow() {
		t.Fatal("expected Allow()=false after burst exhausted")
	}
}

func TestRPSRefill(t *testing.T) {
	tb := ratelimit.NewTokenBucket(10, 1) // 10 RPS, burst=1
	// Consume the single token.
	if !tb.Allow() {
		t.Fatal("first Allow should succeed")
	}
	if tb.Allow() {
		t.Fatal("second Allow should fail (empty bucket)")
	}

	// After ~200ms at 10 RPS, we should have ~2 tokens.
	time.Sleep(200 * time.Millisecond)
	if !tb.Allow() {
		t.Error("expected Allow after refill")
	}
}

func TestAdaptiveOn429ReducesDataRPS(t *testing.T) {
	rl := ratelimit.NewAdaptiveRateLimiter(100, 8, 4, 16)

	initial := rl.DataRPS()
	rl.On429()
	after := rl.DataRPS()

	if after >= initial {
		t.Errorf("expected DataRPS to decrease: initial=%f after=%f", initial, after)
	}
}

func TestAdaptiveBackoffDuration(t *testing.T) {
	rl := ratelimit.NewAdaptiveRateLimiter(10, 5, 2, 8)
	d := rl.BackoffDuration()
	if d < time.Second || d > 2*time.Second {
		t.Errorf("unexpected backoff duration: %v", d)
	}
}

func TestAdaptiveOn429Floor(t *testing.T) {
	rl := ratelimit.NewAdaptiveRateLimiter(100, 0.6, 2, 4)
	// Halve repeatedly until we hit the floor.
	for i := 0; i < 10; i++ {
		rl.On429()
	}
	if rl.DataRPS() < 0.5 {
		t.Errorf("DataRPS below floor: %f", rl.DataRPS())
	}
}
