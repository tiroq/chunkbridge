package observability

import "sync/atomic"

// Metrics holds all operational counters for a chunkbridge instance.
type Metrics struct {
	// Transport
	MessagesSent     atomic.Int64
	MessagesReceived atomic.Int64
	MessageDrops     atomic.Int64
	BytesSent        atomic.Int64
	BytesReceived    atomic.Int64

	// Protocol
	FramesEncoded  atomic.Int64
	FramesDecoded  atomic.Int64
	ChunksSent     atomic.Int64
	ChunksReceived atomic.Int64
	ReassemblyOK   atomic.Int64
	ReassemblyFail atomic.Int64

	// HTTP proxy
	ProxyRequests  atomic.Int64
	ProxyResponses atomic.Int64
	ProxyErrors    atomic.Int64

	// Exit executor
	ExitRequests  atomic.Int64
	ExitResponses atomic.Int64
	ExitErrors    atomic.Int64

	// Rate limiting
	RateLimitHits  atomic.Int64
	Backoffs       atomic.Int64
	RateLimitOn429 atomic.Int64
}

// NewMetrics creates a zeroed Metrics struct.
func NewMetrics() *Metrics {
	return &Metrics{}
}
