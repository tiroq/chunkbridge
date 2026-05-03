package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/tiroq/chunkbridge/internal/config"
	"github.com/tiroq/chunkbridge/internal/observability"
	"github.com/tiroq/chunkbridge/internal/policy"
	"github.com/tiroq/chunkbridge/internal/protocol"
	"github.com/tiroq/chunkbridge/internal/relay"
	"github.com/tiroq/chunkbridge/internal/transport"
)

// relayRequest is the serialized form of an HTTP request sent through the relay.
type relayRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body,omitempty"`
}

// relayResponse is the serialized form of an HTTP response received via relay.
type relayResponse struct {
	StatusCode int                 `json:"status"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body,omitempty"`
}

// HTTPProxy listens on 127.0.0.1 and forwards HTTP requests through the relay.
type HTTPProxy struct {
	session *relay.Session
	pol     *policy.Policy
	cfg     config.Config
	metrics *observability.Metrics
	log     *observability.Logger
	server  *http.Server
}

// NewHTTPProxy creates an HTTPProxy that uses t for transport and key for encryption.
func NewHTTPProxy(t transport.Transport, key []byte, cfg config.Config) *HTTPProxy {
	sessionID := fmt.Sprintf("proxy-%d", time.Now().UnixNano())
	p := &HTTPProxy{
		session: relay.NewSession(sessionID, t, key),
		pol:     policy.New(cfg.Policy),
		cfg:     cfg,
		metrics: observability.NewMetrics(),
		log:     observability.NewLogger(cfg.Log.Level, cfg.Log.Format),
	}
	return p
}

// WithRateLimiter wires a DataLimiter into the proxy's relay session so that
// every outbound DATA chunk is throttled. Call before Serve.
func (p *HTTPProxy) WithRateLimiter(lim relay.DataLimiter) {
	p.session.WithRateLimiter(lim)
}

// Serve starts accepting connections on ln and blocks until ln is closed.
// It also starts the background response dispatcher.
func (p *HTTPProxy) Serve(ln net.Listener) error {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := p.session.Start(ctx); err != nil && ctx.Err() == nil {
			p.log.Error("relay session error", "err", err)
		}
		cancel()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.ServeHTTP)
	p.server = &http.Server{Handler: mux}

	err := p.server.Serve(ln)
	cancel()
	return err
}

// ServeHTTP handles a single proxied HTTP request.
func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		http.Error(w, "HTTPS CONNECT tunnelling is not supported by chunkbridge", http.StatusNotImplemented)
		return
	}

	p.metrics.ProxyRequests.Add(1)

	// Read body.
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
	}

	// Build target URL from the request (proxy sends absolute URLs).
	targetURL := r.RequestURI
	if targetURL == "" {
		targetURL = r.URL.String()
	}

	// Apply client-side policy check before forwarding to the exit.
	if err := p.pol.CheckRequest(targetURL); err != nil {
		p.log.Warn("proxy: policy denied", "url", targetURL, "err", err)
		http.Error(w, "policy: "+err.Error(), http.StatusForbidden)
		return
	}

	req := relayRequest{
		Method:  r.Method,
		URL:     targetURL,
		Headers: map[string][]string(r.Header),
		Body:    bodyBytes,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	frame := &protocol.Frame{
		Version:   1,
		Type:      protocol.FrameDATA,
		SessionID: p.session.SessionID(),
		RequestID: newID(),
		Payload:   payload,
	}

	resp, err := p.session.SendRequest(r.Context(), frame, 30*time.Second)
	if err != nil {
		p.metrics.ProxyErrors.Add(1)
		p.log.Error("proxy: relay error", "err", err)
		http.Error(w, fmt.Sprintf("relay error: %v", err), http.StatusBadGateway)
		return
	}

	var relResp relayResponse
	if err := json.Unmarshal(resp.Payload, &relResp); err != nil {
		p.metrics.ProxyErrors.Add(1)
		http.Error(w, "invalid response from exit", http.StatusBadGateway)
		return
	}

	for k, vals := range relResp.Headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(relResp.StatusCode)
	if len(relResp.Body) > 0 {
		_, _ = io.Copy(w, bytes.NewReader(relResp.Body))
	}
	p.metrics.ProxyResponses.Add(1)
}
