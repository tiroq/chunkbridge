package transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// ErrNotImplemented is returned by MaxTransport methods that are not yet wired up.
var ErrNotImplemented = errors.New("transport: not implemented")

// MaxTransport is a skeleton Transport that will communicate via the Max.ai
// messaging API. It compiles but returns ErrNotImplemented until the API
// endpoints are known.
type MaxTransport struct {
	token      string
	fromHandle string
	toHandle   string
	client     *http.Client
	closed     chan struct{}
}

// NewMaxTransport creates a MaxTransport, reading the API token from the
// environment variable named by tokenEnv.
func NewMaxTransport(tokenEnv, fromHandle, toHandle string) (*MaxTransport, error) {
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("transport: max: API token environment variable is not set")
	}
	return &MaxTransport{
		token:      token,
		fromHandle: fromHandle,
		toHandle:   toHandle,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		closed: make(chan struct{}),
	}, nil
}

// Send transmits a message via the Max API.
// TODO: implement actual API call once endpoints are available.
func (m *MaxTransport) Send(_ context.Context, _ Message) error {
	return ErrNotImplemented
}

// Receive returns a channel of incoming messages from the Max API.
// TODO: implement polling/webhook once endpoints are available.
func (m *MaxTransport) Receive(_ context.Context) (<-chan Message, error) {
	return nil, ErrNotImplemented
}

// Close shuts down the transport.
func (m *MaxTransport) Close() error {
	select {
	case <-m.closed:
	default:
		close(m.closed)
	}
	return nil
}
