package transport

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// MemoryOptions configures the in-process memory transport.
type MemoryOptions struct {
	// LatencyMs adds artificial send latency in milliseconds.
	LatencyMs int
	// DropRate is the probability [0,1) of silently dropping a message.
	DropRate float64
}

// MemoryTransport is an in-process Transport used for testing.
type MemoryTransport struct {
	send    chan Message
	receive chan Message
	opts    MemoryOptions
	once    sync.Once
	closed  chan struct{}
	peer    *MemoryTransport // reference to the paired transport
}

// NewMemoryPair creates two connected MemoryTransports.
// Messages sent on A arrive on B, and vice-versa.
func NewMemoryPair(opts MemoryOptions) (*MemoryTransport, *MemoryTransport) {
	aToB := make(chan Message, 256)
	bToA := make(chan Message, 256)

	a := &MemoryTransport{send: aToB, receive: bToA, opts: opts, closed: make(chan struct{})}
	b := &MemoryTransport{send: bToA, receive: aToB, opts: opts, closed: make(chan struct{})}
	a.peer = b
	b.peer = a
	return a, b
}

// Send transmits a message to the paired transport.
func (m *MemoryTransport) Send(ctx context.Context, msg Message) error {
	if m.opts.DropRate > 0 && rand.Float64() < m.opts.DropRate {
		return nil
	}
	if m.opts.LatencyMs > 0 {
		select {
		case <-time.After(time.Duration(m.opts.LatencyMs) * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case m.send <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.closed:
		return ErrClosed
	}
}

// Receive returns the channel on which incoming messages are delivered.
func (m *MemoryTransport) Receive(ctx context.Context) (<-chan Message, error) {
	ch := make(chan Message, 256)
	go func() {
		defer close(ch)
		for {
			select {
			case msg, ok := <-m.receive:
				if !ok {
					return
				}
				select {
				case ch <- msg:
				case <-ctx.Done():
					return
				case <-m.closed:
					return
				}
			case <-ctx.Done():
				return
			case <-m.closed:
				return
			}
		}
	}()
	return ch, nil
}

// Close stops the transport and signals the paired transport so blocked senders
// do not deadlock after the buffer fills.
func (m *MemoryTransport) Close() error {
	m.once.Do(func() { close(m.closed) })
	if p := m.peer; p != nil {
		p.once.Do(func() { close(p.closed) })
	}
	return nil
}

// ErrClosed is returned when an operation is attempted on a closed transport.
var ErrClosed = errClosed("transport: closed")

type errClosed string

func (e errClosed) Error() string { return string(e) }
