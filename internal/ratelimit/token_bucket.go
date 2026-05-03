package ratelimit

import (
	"sync"
	"time"
)

// TokenBucket implements a token-bucket rate limiter.
type TokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	maxBurst float64
	rps      float64
	lastTime time.Time
}

// NewTokenBucket creates a TokenBucket that refills at rps tokens per second
// with a maximum burst of burst tokens.
func NewTokenBucket(rps float64, burst int) *TokenBucket {
	return &TokenBucket{
		tokens:   float64(burst),
		maxBurst: float64(burst),
		rps:      rps,
		lastTime: time.Now(),
	}
}

// Allow returns true if a token is available and consumes it.
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	tb.lastTime = now

	tb.tokens += elapsed * tb.rps
	if tb.tokens > tb.maxBurst {
		tb.tokens = tb.maxBurst
	}

	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}

// RPS returns the configured refill rate.
func (tb *TokenBucket) RPS() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.rps
}

// SetRPS updates the refill rate of the bucket.
func (tb *TokenBucket) SetRPS(rps float64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.rps = rps
}
