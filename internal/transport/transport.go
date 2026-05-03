package transport

import (
	"context"
	"time"
)

// Message is an opaque text message exchanged via a transport.
type Message struct {
	ID        string
	From      string
	To        string
	Text      string
	CreatedAt time.Time
}

// Transport is the interface for sending and receiving messages.
type Transport interface {
	// Send transmits a message. It blocks until the message is accepted or ctx
	// is cancelled.
	Send(ctx context.Context, msg Message) error

	// Receive returns a channel that delivers incoming messages. The channel is
	// closed when the transport is closed or ctx is cancelled.
	Receive(ctx context.Context) (<-chan Message, error)

	// Close shuts down the transport and releases resources.
	Close() error
}
