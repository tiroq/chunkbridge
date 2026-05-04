package maxtransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tiroq/relaykit/pkg/transport"
)

// ─── dedupeSet ───────────────────────────────────────────────────────────────

// dedupeSet is a bounded deduplication window backed by a map and a FIFO
// eviction queue. It provides O(1) lookup and amortised O(1) insertion.
// When the window is full the oldest entry is evicted, so a message whose ID
// was evicted may be re-delivered — this is documented and acceptable.
//
// dedupeSet is not safe for concurrent use; callers must hold their own lock.
type dedupeSet struct {
	ids  map[string]struct{}
	fifo []string
	max  int
}

func newDedupeSet(max int) *dedupeSet {
	return &dedupeSet{
		ids:  make(map[string]struct{}, max),
		fifo: make([]string, 0, max),
		max:  max,
	}
}

// seen reports whether id is in the current window.
func (d *dedupeSet) seen(id string) bool {
	_, ok := d.ids[id]
	return ok
}

// add records id in the window, evicting the oldest entry if necessary.
func (d *dedupeSet) add(id string) {
	if len(d.fifo) >= d.max {
		oldest := d.fifo[0]
		d.fifo = d.fifo[1:]
		delete(d.ids, oldest)
	}
	d.ids[id] = struct{}{}
	d.fifo = append(d.fifo, id)
}

// ─── errors ───────────────────────────────────────────────────────────────────

// RateLimitError is returned by MaxTransport.Send when the API responds with
// HTTP 429 Too Many Requests.
type RateLimitError struct {
	// RetryAfter is the server-requested delay parsed from the Retry-After
	// response header. Zero means the header was absent or unparseable.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("transport: max: rate limited; retry after %s", e.RetryAfter)
	}
	return "transport: max: rate limited"
}

// authError is a non-retryable error from an API call (HTTP 401 or 403).
type authError struct{ code int }

func (e *authError) Error() string {
	return fmt.Sprintf("transport: max: authentication error (HTTP %d); check token configuration", e.code)
}

// ─── config ───────────────────────────────────────────────────────────────────

// MaxTransportConfig holds all construction-time settings for MaxTransport.
type MaxTransportConfig struct {
	// BaseURL is the root URL of the MAX Bot API (no trailing slash).
	// Example: "https://api.max.example.com/v1". Required.
	BaseURL string
	// TokenEnv is the name of the environment variable that holds the bearer
	// token. Required.
	TokenEnv string
	// PeerChatID is the chat ID of the remote chunkbridge endpoint. Required.
	PeerChatID string
	// FromHandle is the handle/ID of this endpoint. Messages whose "from"
	// field matches this value are filtered out on receive to prevent echo.
	FromHandle string
	// PollIntervalMs is the delay in ms between poll requests when the last
	// response was empty. Default: 1000.
	PollIntervalMs int
	// PollTimeoutSec is the server-side long-poll timeout sent as the
	// ?timeout query parameter. Default: 20.
	PollTimeoutSec int
	// SafeChars is the maximum allowed rune count of outbound message text.
	// 0 means no limit is enforced at the transport layer.
	SafeChars int
	// DedupeMaxIDs is the capacity of the receive deduplication window.
	// When the window is full the oldest ID is evicted; messages whose ID was
	// evicted may be re-delivered. Must be > 0. Default applied by NewMaxTransport: 4096.
	DedupeMaxIDs int
}

// ─── transport ────────────────────────────────────────────────────────────────

// MaxTransport is a Transport adapter for the Max.ai Bot API.
//
// Assumed API endpoints (documented in docs/max-transport.md):
//
//	POST <BaseURL>/messages          → send a text message
//	GET  <BaseURL>/messages/poll     → long-poll for incoming messages
type MaxTransport struct {
	baseURL        string
	token          string
	peerChatID     string
	fromHandle     string
	pollInterval   time.Duration
	pollTimeoutSec int
	safeChars      int
	dedupeMaxIDs   int
	client         *http.Client
	closed         chan struct{}
	closeOnce      sync.Once
	receiveStarted atomic.Bool
	on429          func() // optional; called when HTTP 429 is received
}

// NewMaxTransport creates a MaxTransport from cfg. The bearer token is read
// from the environment variable named by cfg.TokenEnv at construction time and
// is never written to logs or error messages.
func NewMaxTransport(cfg MaxTransportConfig) (*MaxTransport, error) {
	token := os.Getenv(cfg.TokenEnv)
	if token == "" {
		return nil, fmt.Errorf("config: environment variable %s is not set", cfg.TokenEnv)
	}

	pollInterval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	if pollInterval <= 0 {
		pollInterval = time.Second
	}

	pollTimeoutSec := cfg.PollTimeoutSec
	if pollTimeoutSec <= 0 {
		pollTimeoutSec = 20
	}

	// HTTP client timeout must exceed the server-side long-poll timeout so
	// that an empty-response poll does not trigger a client deadline error.
	httpTimeout := time.Duration(pollTimeoutSec+10) * time.Second

	dedupeMaxIDs := cfg.DedupeMaxIDs
	if dedupeMaxIDs <= 0 {
		dedupeMaxIDs = 4096
	}

	return &MaxTransport{
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		token:          token,
		peerChatID:     cfg.PeerChatID,
		fromHandle:     cfg.FromHandle,
		pollInterval:   pollInterval,
		pollTimeoutSec: pollTimeoutSec,
		safeChars:      cfg.SafeChars,
		dedupeMaxIDs:   dedupeMaxIDs,
		client:         &http.Client{Timeout: httpTimeout},
		closed:         make(chan struct{}),
	}, nil
}

// WithOn429 registers a callback that is invoked whenever the API returns HTTP
// 429. Intended for wiring AdaptiveRateLimiter.On429(). Returns m so it can be
// chained after construction.
func (m *MaxTransport) WithOn429(fn func()) *MaxTransport {
	m.on429 = fn
	return m
}

// WithHTTPClient replaces the internal HTTP client. Intended for testing with
// custom round-trippers (e.g., to assert response body closure). Returns m for
// chaining.
func (m *MaxTransport) WithHTTPClient(c *http.Client) *MaxTransport {
	m.client = c
	return m
}

//
// The JSON shapes below represent the assumed MAX Bot API contract.
// See docs/max-transport.md §"Assumed API JSON shapes" for details.

// maxSendRequest is the JSON body for POST /messages.
type maxSendRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// maxPollResponse is the JSON body returned by GET /messages/poll.
type maxPollResponse struct {
	Messages []maxAPIMessage `json:"messages"`
}

// maxAPIMessage is a single entry in a poll response.
type maxAPIMessage struct {
	MessageID string `json:"message_id"`
	From      string `json:"from"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"` // RFC 3339; empty falls back to time.Now()
}

// ─── Send ─────────────────────────────────────────────────────────────────────

// Send transmits msg via POST <BaseURL>/messages.
//
// Errors are prefixed with "transport: max:". A *RateLimitError is returned on
// HTTP 429 and includes the parsed Retry-After duration if the header is present.
func (m *MaxTransport) Send(ctx context.Context, msg transport.Message) error {
	if msg.Text == "" {
		return fmt.Errorf("transport: max: message text must not be empty")
	}
	if m.safeChars > 0 && len([]rune(msg.Text)) > m.safeChars {
		return fmt.Errorf("transport: max: message length %d runes exceeds safe limit %d",
			len([]rune(msg.Text)), m.safeChars)
	}

	body, err := json.Marshal(maxSendRequest{ChatID: m.peerChatID, Text: msg.Text})
	if err != nil {
		return fmt.Errorf("transport: max: marshal send request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("transport: max: build send request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("transport: max: send: %w", err)
	}
	defer resp.Body.Close()

	return m.checkStatus(resp, "send")
}

// ─── Receive ──────────────────────────────────────────────────────────────────

// Receive starts an internal polling goroutine and returns a buffered channel
// of incoming messages. The channel is closed when ctx is cancelled or Close
// is called.
//
// Receive must be called at most once per MaxTransport instance. A second call
// returns an error without starting a goroutine.
func (m *MaxTransport) Receive(ctx context.Context) (<-chan transport.Message, error) {
	if !m.receiveStarted.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("transport: max: receive already started")
	}
	ch := make(chan transport.Message, 256)
	go m.pollLoop(ctx, ch)
	return ch, nil
}

// pollLoop polls GET /messages/poll until ctx is done or the transport is closed.
func (m *MaxTransport) pollLoop(ctx context.Context, ch chan<- transport.Message) {
	defer close(ch)

	// pollCtx is cancelled when either the caller's ctx is done or the
	// transport is closed, so in-flight HTTP requests unblock promptly on Close.
	pollCtx, pollCancel := context.WithCancel(ctx)
	defer pollCancel()
	go func() {
		select {
		case <-m.closed:
			pollCancel()
		case <-pollCtx.Done():
		}
	}()

	dedupe := newDedupeSet(m.dedupeMaxIDs)
	var afterID string

	for {
		if pollCtx.Err() != nil {
			return
		}

		messages, err := m.pollOnce(pollCtx, afterID)
		if err != nil {
			switch {
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				return
			}
			var ae *authError
			if errors.As(err, &ae) {
				// Non-retryable: bad credentials. Stop polling immediately.
				return
			}
			var rlErr *RateLimitError
			if errors.As(err, &rlErr) {
				if m.on429 != nil {
					m.on429()
				}
				delay := rlErr.RetryAfter
				if delay <= 0 {
					delay = m.pollInterval
				}
				select {
				case <-time.After(delay):
				case <-pollCtx.Done():
					return
				}
				continue
			}
			// Network / 5xx errors: wait one poll interval then retry.
			select {
			case <-time.After(m.pollInterval):
			case <-pollCtx.Done():
				return
			}
			continue
		}

		for _, apiMsg := range messages {
			// Deduplicate by stable message ID using the bounded FIFO window.
			if apiMsg.MessageID != "" {
				if dedupe.seen(apiMsg.MessageID) {
					continue
				}
				dedupe.add(apiMsg.MessageID)
				afterID = apiMsg.MessageID
			}

			// Filter echo: skip messages sent by this endpoint.
			if m.fromHandle != "" && apiMsg.From == m.fromHandle {
				continue
			}

			// Parse timestamp; fall back to now if absent or malformed.
			ts := time.Now()
			if apiMsg.CreatedAt != "" {
				if t, parseErr := time.Parse(time.RFC3339, apiMsg.CreatedAt); parseErr == nil {
					ts = t
				}
			}

			out := transport.Message{
				ID:        apiMsg.MessageID,
				From:      apiMsg.From,
				Text:      apiMsg.Text,
				CreatedAt: ts,
			}

			select {
			case ch <- out:
			case <-pollCtx.Done():
				return
			}
		}

		// If the response was empty, wait before the next poll to avoid busy-looping.
		if len(messages) == 0 {
			select {
			case <-time.After(m.pollInterval):
			case <-pollCtx.Done():
				return
			}
		}
	}
}

// pollOnce performs a single GET /messages/poll request.
func (m *MaxTransport) pollOnce(ctx context.Context, afterID string) ([]maxAPIMessage, error) {
	u, err := url.Parse(m.baseURL + "/messages/poll")
	if err != nil {
		return nil, fmt.Errorf("transport: max: parse poll URL: %w", err)
	}
	q := u.Query()
	q.Set("chat_id", m.peerChatID)
	q.Set("timeout", strconv.Itoa(m.pollTimeoutSec))
	if afterID != "" {
		q.Set("after_id", afterID)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("transport: max: build poll request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.token)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transport: max: poll: %w", err)
	}
	defer resp.Body.Close()

	if err := m.checkStatus(resp, "poll"); err != nil {
		return nil, err
	}

	var pr maxPollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("transport: max: decode poll response: %w", err)
	}
	return pr.Messages, nil
}

// ─── Close ────────────────────────────────────────────────────────────────────

// Close stops the transport and signals any running poll goroutine to exit.
func (m *MaxTransport) Close() error {
	m.closeOnce.Do(func() { close(m.closed) })
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// checkStatus translates non-2xx HTTP responses into typed errors.
// op is a short label ("send", "poll") used in error messages.
func (m *MaxTransport) checkStatus(resp *http.Response, op string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &authError{code: resp.StatusCode}
	case http.StatusNotFound:
		return fmt.Errorf("transport: max: %s: endpoint not found (HTTP 404); check base_url and peer_chat_id", op)
	case http.StatusRequestEntityTooLarge:
		return fmt.Errorf("transport: max: %s: message rejected as too large by server (HTTP 413)", op)
	case http.StatusTooManyRequests:
		ra := parseRetryAfter(resp.Header.Get("Retry-After"))
		if m.on429 != nil {
			m.on429()
		}
		return &RateLimitError{RetryAfter: ra}
	default:
		return fmt.Errorf("transport: max: %s: unexpected HTTP %d", op, resp.StatusCode)
	}
}

// parseRetryAfter parses a Retry-After header value (integer seconds or an
// HTTP-date). Returns zero if the header is absent or unparseable.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
