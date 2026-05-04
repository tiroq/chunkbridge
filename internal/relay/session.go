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

// DataLimiter is satisfied by any token-bucket that gates DATA frame sends.
// Both *ratelimit.TokenBucket and *ratelimit.AdaptiveRateLimiter implement it.
type DataLimiter interface {
	AllowData() bool
}

// WaitForToken blocks until the limiter grants a token or ctx is cancelled.
// It sleeps 5 ms between polls to avoid busy-spinning.
func WaitForToken(ctx context.Context, lim DataLimiter) error {
	for {
		if lim.AllowData() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Session manages request/response correlation over a transport.
// It sends frames and matches response frames back to waiting callers.
type Session struct {
	sessionID   string
	t           transport.Transport
	key         []byte
	pending     map[string]chan *protocol.Frame
	mu          sync.Mutex
	seqNum      atomic.Uint32
	reassembler *protocol.Reassembler
	limiter     DataLimiter // optional; nil disables throttling
	maxPending  int         // 0 = unlimited (legacy); >0 = bounded
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

// WithRateLimiter returns a new Session with the given DataLimiter wired into
// the send path. Every outbound DATA chunk will wait for a token before being
// passed to Transport.Send.
func (s *Session) WithRateLimiter(lim DataLimiter) *Session {
	s.limiter = lim
	return s
}

// WithMaxPendingRequests limits how many concurrent in-flight requests the
// session will accept. If n <= 0 the limit is disabled (existing behaviour).
// When the limit is reached, SendRequest returns immediately with:
//
//	relay: too many concurrent requests
func (s *Session) WithMaxPendingRequests(n int) *Session {
	s.maxPending = n
	return s
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
	// Check the concurrency limit atomically with the insert.
	ch := make(chan *protocol.Frame, 1)
	s.mu.Lock()
	if s.maxPending > 0 && len(s.pending) >= s.maxPending {
		s.mu.Unlock()
		return nil, fmt.Errorf("relay: too many concurrent requests")
	}
	s.pending[frame.RequestID] = ch
	s.mu.Unlock()

	// Always remove the pending entry when this call returns, regardless of
	// how it exits (success, timeout, context cancel, send error).
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
		if resp.Type == protocol.FrameERROR {
			ep, err := protocol.UnmarshalErrorPayload(resp.Payload)
			if err != nil {
				return nil, fmt.Errorf("relay: malformed error frame: %w", err)
			}
			return nil, &RelayError{Code: ep.Code, HTTPStatus: ep.HTTPStatus, Message: ep.Message}
		}
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
		if s.limiter != nil {
			if err := WaitForToken(ctx, s.limiter); err != nil {
				return fmt.Errorf("relay: rate limit: %w", err)
			}
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
