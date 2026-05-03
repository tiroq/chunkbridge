package protocol

import (
	"bytes"
	"fmt"
	"sync"
	"time"
)

// assemblyKey uniquely identifies a multi-chunk message.
type assemblyKey struct {
	sessionID string
	requestID string
}

// assembly collects incoming chunks for a single logical message.
type assembly struct {
	frames    map[uint32]*Frame
	total     uint32
	createdAt time.Time
}

// Reassembler collects protocol frames and returns complete messages once all
// chunks have arrived. It is safe for concurrent use.
type Reassembler struct {
	mu         sync.Mutex
	assemblies map[assemblyKey]*assembly
	timeout    time.Duration
}

// NewReassembler creates a Reassembler that discards incomplete assemblies
// after the given timeout.
func NewReassembler(timeout time.Duration) *Reassembler {
	r := &Reassembler{
		assemblies: make(map[assemblyKey]*assembly),
		timeout:    timeout,
	}
	return r
}

// Add stores a frame and returns the reassembled frame (with the full payload)
// once all chunks have been received. Returns (nil, false) if more chunks are
// still outstanding.
func (r *Reassembler) Add(frame *Frame) (*Frame, bool) {
	// Single-chunk fast path.
	if frame.TotalChunks <= 1 {
		out := *frame
		out.TotalChunks = 1
		out.ChunkIndex = 0
		return &out, true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.evictExpired()

	key := assemblyKey{sessionID: frame.SessionID, requestID: frame.RequestID}
	a, ok := r.assemblies[key]
	if !ok {
		a = &assembly{
			frames:    make(map[uint32]*Frame),
			total:     frame.TotalChunks,
			createdAt: time.Now(),
		}
		r.assemblies[key] = a
	}

	// Ignore duplicates.
	if _, exists := a.frames[frame.ChunkIndex]; exists {
		return nil, false
	}
	a.frames[frame.ChunkIndex] = frame

	if uint32(len(a.frames)) < a.total {
		return nil, false
	}

	// All chunks received – reassemble in order.
	delete(r.assemblies, key)

	var buf bytes.Buffer
	for i := uint32(0); i < a.total; i++ {
		f, ok := a.frames[i]
		if !ok {
			return nil, false
		}
		buf.Write(f.Payload)
	}

	// Use the first chunk's metadata as the base frame.
	out := *a.frames[0]
	out.Payload = buf.Bytes()
	out.TotalChunks = 1
	out.ChunkIndex = 0
	return &out, true
}

// evictExpired removes assemblies that have exceeded the timeout.
// Must be called with r.mu held.
func (r *Reassembler) evictExpired() {
	if r.timeout <= 0 {
		return
	}
	now := time.Now()
	for k, a := range r.assemblies {
		if now.Sub(a.createdAt) > r.timeout {
			delete(r.assemblies, k)
		}
	}
}

// ErrTimeout is returned when a reassembly expires before completion.
var ErrTimeout = fmt.Errorf("reassembly: timed out waiting for chunks")
