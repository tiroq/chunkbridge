package ratelimit

import (
	"math/rand"
	"sync"
	"time"
)

// AdaptiveRateLimiter manages three token buckets (global, data, control) and
// backs off the data bucket when a 429 response is received.
type AdaptiveRateLimiter struct {
	mu      sync.Mutex
	global  *TokenBucket
	data    *TokenBucket
	control *TokenBucket
	dataRPS float64
	burst   int
}

// NewAdaptiveRateLimiter creates an AdaptiveRateLimiter with the given initial rates.
func NewAdaptiveRateLimiter(globalRPS, dataRPS, controlRPS float64, burst int) *AdaptiveRateLimiter {
	return &AdaptiveRateLimiter{
		global:  NewTokenBucket(globalRPS, burst),
		data:    NewTokenBucket(dataRPS, burst),
		control: NewTokenBucket(controlRPS, burst),
		dataRPS: dataRPS,
		burst:   burst,
	}
}

// AllowData returns true if both the global and data buckets have capacity.
func (a *AdaptiveRateLimiter) AllowData() bool {
	return a.global.Allow() && a.data.Allow()
}

// AllowControl returns true if both the global and control buckets have capacity.
func (a *AdaptiveRateLimiter) AllowControl() bool {
	return a.global.Allow() && a.control.Allow()
}

// On429 is called when the transport reports a 429 Too Many Requests error.
// It halves the data RPS (floor: 0.5) and rebuilds the data bucket.
func (a *AdaptiveRateLimiter) On429() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.dataRPS = a.dataRPS * 0.5
	if a.dataRPS < 0.5 {
		a.dataRPS = 0.5
	}
	// Intentionally set burst to 1 after a 429 to force conservative sends
	// until the bucket naturally refills at the reduced rate.
	a.data = NewTokenBucket(a.dataRPS, 1)
}

// BackoffDuration returns a jittered backoff duration suitable for retry delays.
func (a *AdaptiveRateLimiter) BackoffDuration() time.Duration {
	base := time.Second
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
}

// DataRPS returns the current data bucket RPS.
func (a *AdaptiveRateLimiter) DataRPS() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dataRPS
}
