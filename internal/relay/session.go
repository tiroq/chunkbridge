package relay

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tiroq/chunkbridge/internal/protocol"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// Session manages request/response correlation over a transport.
// It sends frames and matches response frames back to waiting callers.
type Session struct {
	sessionID string
	t         transport.Transport
	key       []byte
	pending   map[string]chan *protocol.Frame
	mu        sync.Mutex
	seqNum    atomic.Uint32
	reassembler *protocol.Reassembler
}

// SessionID returns the session identifier for this Session.
func (s *Session) SessionID() string { return s.sessionID }

// NewSession creates a relay Session bound to the given transport and key.
func NewSession(sessionID string, t transport.Transport, key []byte) *Session {
	return &Session{
		sessionID:   sessionID,
		t:           t,
		key:         key,
		pending:     make(map[string]chan *protocol.Frame),
		reassembler: protocol.NewReassembler(60 * time.Second),
	}
}

// Start begins reading from the transport and dispatching responses to pending
// waiters. It blocks until ctx is cancelled.
func (s *Session) Start(ctx context.Context) error {
	msgCh, err := s.t.Receive(ctx)
	if err != nil {
		return fmt.Errorf("relay: receive: %w", err)
	}
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			frame, err := protocol.DecodeMessage(msg.Text, s.key)
			if err != nil {
				continue
			}
			complete, ok := s.reassembler.Add(frame)
			if !ok {
				continue
			}
			s.dispatch(complete)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// dispatch routes a completed frame to the waiting caller.
func (s *Session) dispatch(frame *protocol.Frame) {
	s.mu.Lock()
	ch, ok := s.pending[frame.RequestID]
	s.mu.Unlock()
	if ok {
		select {
		case ch <- frame:
		default:
		}
	}
}

// SendRequest encodes and sends all chunks of frame, then waits for a response.
func (s *Session) SendRequest(ctx context.Context, frame *protocol.Frame, timeout time.Duration) (*protocol.Frame, error) {
	// Register the response channel before sending.
	ch := make(chan *protocol.Frame, 1)
	s.mu.Lock()
	s.pending[frame.RequestID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, frame.RequestID)
		s.mu.Unlock()
	}()

	// Chunk and send.
	if err := s.sendFrame(ctx, frame); err != nil {
		return nil, err
	}

	// Wait for response.
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return resp, nil
	case <-timer.C:
		return nil, fmt.Errorf("relay: timeout waiting for response to request %s", frame.RequestID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// sendFrame chunks and encodes a frame, then sends all messages via the transport.
func (s *Session) sendFrame(ctx context.Context, frame *protocol.Frame) error {
	chunks := protocol.Chunk(*frame, protocol.MaxPayloadBytes)
	for _, chunk := range chunks {
		c := chunk
		c.SeqNum = s.seqNum.Add(1)
		text, err := protocol.EncodeMessage(&c, s.key)
		if err != nil {
			return fmt.Errorf("relay: encode: %w", err)
		}
		if err := s.t.Send(ctx, transport.Message{Text: text}); err != nil {
			return fmt.Errorf("relay: send: %w", err)
		}
	}
	return nil
}

// SendResponse encodes and sends a response frame (no waiting for reply).
func (s *Session) SendResponse(ctx context.Context, frame *protocol.Frame) error {
	return s.sendFrame(ctx, frame)
}
