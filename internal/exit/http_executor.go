package exit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tiroq/chunkbridge/internal/config"
	"github.com/tiroq/chunkbridge/internal/observability"
	"github.com/tiroq/chunkbridge/internal/policy"
	"github.com/tiroq/chunkbridge/internal/protocol"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// relayRequest mirrors the proxy's relayRequest struct.
type relayRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body,omitempty"`
}

// relayResponse mirrors the proxy's relayResponse struct.
type relayResponse struct {
	StatusCode int                 `json:"status"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body,omitempty"`
}

// hopByHopHeaders lists headers that must not be forwarded to upstream servers
// per RFC 7230 §6.1. Custom hop-by-hop headers named in the Connection header
// are handled separately in handleRequest.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"TE",
	"Trailer",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

// HTTPExecutor receives relay requests, makes outbound HTTP calls, and sends
// the responses back via the transport.
type HTTPExecutor struct {
	t           transport.Transport
	key         []byte
	pol         *policy.Policy
	cfg         config.Config
	client      *http.Client
	metrics     *observability.Metrics
	log         *observability.Logger
	seqNum      atomic.Uint32
	reassembler *protocol.Reassembler
	sessionID   string
}

// NewHTTPExecutor creates an HTTPExecutor using t as the transport.
func NewHTTPExecutor(t transport.Transport, key []byte, cfg config.Config) *HTTPExecutor {
	safeDialer := policy.NewSafeDialer(
		net.DefaultResolver,
		cfg.Policy.BlockPrivateRanges,
	)
	httpTransport := &http.Transport{
		DialContext:         safeDialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &HTTPExecutor{
		t:   t,
		key: key,
		pol: policy.New(cfg.Policy),
		cfg: cfg,
		client: &http.Client{
			Transport: httpTransport,
			Timeout:   time.Duration(cfg.Exit.RequestTimeoutSec) * time.Second,
		},
		metrics:     observability.NewMetrics(),
		log:         observability.NewLogger(cfg.Log.Level, cfg.Log.Format),
		reassembler: protocol.NewReassembler(60 * time.Second),
		sessionID:   fmt.Sprintf("exit-%d", time.Now().UnixNano()),
	}
}

// Run receives messages from the transport and processes them until ctx is cancelled.
func (e *HTTPExecutor) Run(ctx context.Context) error {
	msgCh, err := e.t.Receive(ctx)
	if err != nil {
		return fmt.Errorf("exit: receive: %w", err)
	}

	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			frame, err := protocol.DecodeMessage(msg.Text, e.key)
			if err != nil {
				e.log.Debug("exit: decode error", "err", err)
				continue
			}

			complete, ok := e.reassembler.Add(frame)
			if !ok {
				continue
			}

			go e.handleRequest(ctx, complete)

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// handleRequest processes a single completed request frame.
func (e *HTTPExecutor) handleRequest(ctx context.Context, frame *protocol.Frame) {
	e.metrics.ExitRequests.Add(1)

	var req relayRequest
	if err := json.Unmarshal(frame.Payload, &req); err != nil {
		e.log.Error("exit: unmarshal request", "err", err)
		e.sendError(ctx, frame, http.StatusBadRequest, "invalid request payload")
		return
	}

	if err := e.pol.CheckRequest(req.URL); err != nil {
		e.log.Warn("exit: policy denied", "url", req.URL, "err", err)
		e.sendError(ctx, frame, http.StatusForbidden, err.Error())
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		e.sendError(ctx, frame, http.StatusBadRequest, fmt.Sprintf("build request: %v", err))
		return
	}
	for k, vals := range req.Headers {
		for _, v := range vals {
			httpReq.Header.Add(k, v)
		}
	}
	// Strip hop-by-hop headers before forwarding upstream per RFC 7230 §6.1.
	// First, honour any custom hop-by-hop headers listed in the Connection header.
	if conn := httpReq.Header.Get("Connection"); conn != "" {
		for _, h := range strings.Split(conn, ",") {
			httpReq.Header.Del(strings.TrimSpace(h))
		}
	}
	for _, h := range hopByHopHeaders {
		httpReq.Header.Del(h)
	}

	httpResp, err := e.client.Do(httpReq)
	if err != nil {
		e.log.Error("exit: outbound request", "err", err)
		e.sendError(ctx, frame, http.StatusBadGateway, fmt.Sprintf("outbound: %v", err))
		return
	}
	defer httpResp.Body.Close()

	// Enforce content-type policy before reading the potentially large body.
	if ct := httpResp.Header.Get("Content-Type"); ct != "" {
		if err := policy.CheckContentType(ct, e.cfg.Policy); err != nil {
			e.log.Warn("exit: content-type policy denied", "content_type", ct)
			e.sendError(ctx, frame, http.StatusForbidden, err.Error())
			return
		}
	}

	maxBytes := e.cfg.Policy.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(httpResp.Body, maxBytes+1))
	if err != nil {
		e.sendError(ctx, frame, http.StatusBadGateway, fmt.Sprintf("read body: %v", err))
		return
	}
	if int64(len(body)) > maxBytes {
		e.sendError(ctx, frame, http.StatusBadGateway, "response too large")
		return
	}

	rel := relayResponse{
		StatusCode: httpResp.StatusCode,
		Headers:    map[string][]string(httpResp.Header),
		Body:       body,
	}

	payload, err := json.Marshal(rel)
	if err != nil {
		e.sendError(ctx, frame, http.StatusInternalServerError, "marshal response")
		return
	}

	respFrame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: frame.SessionID,
		RequestID: frame.RequestID,
		Payload:   payload,
	}

	if err := e.sendFrame(ctx, respFrame); err != nil {
		e.log.Error("exit: send response", "err", err)
		return
	}
	e.metrics.ExitResponses.Add(1)
}

// sendError sends a relay error response back to the proxy.
func (e *HTTPExecutor) sendError(ctx context.Context, req *protocol.Frame, status int, msg string) {
	rel := relayResponse{
		StatusCode: status,
		Body:       []byte(msg),
	}
	payload, _ := json.Marshal(rel)
	frame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: req.SessionID,
		RequestID: req.RequestID,
		Payload:   payload,
	}
	_ = e.sendFrame(ctx, frame)
	e.metrics.ExitErrors.Add(1)
}

// sendFrame chunks and encodes a frame, then sends all parts via transport.
func (e *HTTPExecutor) sendFrame(ctx context.Context, frame *protocol.Frame) error {
	chunks := protocol.Chunk(*frame, protocol.MaxPayloadBytes)
	for _, chunk := range chunks {
		c := chunk
		c.SeqNum = e.seqNum.Add(1)
		text, err := protocol.EncodeMessage(&c, e.key)
		if err != nil {
			return fmt.Errorf("exit: encode: %w", err)
		}
		if err := e.t.Send(ctx, transport.Message{Text: text}); err != nil {
			return fmt.Errorf("exit: send: %w", err)
		}
	}
	return nil
}
