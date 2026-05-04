package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/tiroq/chunkbridge/internal/cache"
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
	cache   *cache.Cache // nil when disabled
}

// NewHTTPProxy creates an HTTPProxy that uses t for transport and key for encryption.
func NewHTTPProxy(t transport.Transport, key []byte, cfg config.Config) *HTTPProxy {
	sessionID := fmt.Sprintf("proxy-%d", time.Now().UnixNano())
	sess := relay.NewSession(sessionID, t, key)
	if cfg.Proxy.MaxConcurrentRequests > 0 {
		sess.WithMaxPendingRequests(cfg.Proxy.MaxConcurrentRequests)
	}
	p := &HTTPProxy{
		session: sess,
		pol:     policy.New(cfg.Policy),
		cfg:     cfg,
		metrics: observability.NewMetrics(),
		log:     observability.NewLogger(cfg.Log.Level, cfg.Log.Format),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.ServeHTTP)
	p.server = &http.Server{Handler: mux}
	return p
}

// WithRateLimiter wires a DataLimiter into the proxy's relay session so that
// every outbound DATA chunk is throttled. Call before Serve.
func (p *HTTPProxy) WithRateLimiter(lim relay.DataLimiter) {
	p.session.WithRateLimiter(lim)
}

// WithCache attaches an in-memory response cache to the proxy. When set, safe
// fresh GET/HEAD responses are served from cache without hitting the relay.
// Call before Serve.
func (p *HTTPProxy) WithCache(c *cache.Cache) *HTTPProxy {
	p.cache = c
	return p
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

	err := p.server.Serve(ln)
	cancel()
	return err
}

// Shutdown gracefully stops the HTTP server, waiting up to the deadline in ctx
// for in-flight requests to complete.
func (p *HTTPProxy) Shutdown(ctx context.Context) error {
	return p.server.Shutdown(ctx)
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

	// ── Cache lookup ──────────────────────────────────────────────────────────
	if p.cache != nil {
		reqOpts := cache.RequestOptions{
			CacheWithAuthorization: p.cfg.Cache.CacheWithAuthorization,
			CacheWithCookies:       p.cfg.Cache.CacheWithCookies,
		}
		if cache.IsRequestCacheable(r, reqOpts) {
			key := cache.BuildKey(r.Method, targetURL, r.Header)
			if entry, ok := p.cache.Get(key); ok {
				// Cache HIT — serve directly without relay.
				for k, vals := range entry.Header {
					for _, v := range vals {
						w.Header().Add(k, v)
					}
				}
				w.Header().Set("X-Chunkbridge-Cache", "HIT")
				w.WriteHeader(entry.StatusCode)
				if len(entry.Body) > 0 && r.Method != http.MethodHead {
					_, _ = io.Copy(w, bytes.NewReader(entry.Body))
				}
				p.metrics.ProxyResponses.Add(1)
				return
			}
		}
	}

	// ── Relay round-trip ──────────────────────────────────────────────────────
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

	resp, err := p.session.SendRequest(r.Context(), frame, p.requestTimeout())
	if err != nil {
		p.metrics.ProxyErrors.Add(1)
		p.log.Error("proxy: relay error", "err", err)
		// Structured error sent by the exit node via FrameERROR.
		var relErr *relay.RelayError
		if errors.As(err, &relErr) {
			http.Error(w, relErr.Message, relErr.HTTPStatus)
			return
		}
		// Concurrency limit (local, no relay round-trip).
		if err.Error() == "relay: too many concurrent requests" {
			http.Error(w, "too many concurrent requests", http.StatusTooManyRequests)
			return
		}
		http.Error(w, fmt.Sprintf("relay error: %v", err), http.StatusBadGateway)
		return
	}

	var relResp relayResponse
	if err := json.Unmarshal(resp.Payload, &relResp); err != nil {
		p.metrics.ProxyErrors.Add(1)
		http.Error(w, "invalid response from exit", http.StatusBadGateway)
		return
	}

	// ── Cache store (MISS path) ───────────────────────────────────────────────
	if p.cache != nil {
		respOpts := cache.ResponseOptions{
			MaxEntryBytes: p.cfg.Cache.MaxEntryBytes,
			CachePrivate:  p.cfg.Cache.CachePrivate,
		}
		reqOpts := cache.RequestOptions{
			CacheWithAuthorization: p.cfg.Cache.CacheWithAuthorization,
			CacheWithCookies:       p.cfg.Cache.CacheWithCookies,
		}
		respHeader := http.Header(relResp.Headers)
		bodySize := int64(len(relResp.Body))
		if cache.IsRequestCacheable(r, reqOpts) &&
			cache.IsResponseCacheable(r.Method, relResp.StatusCode, respHeader, bodySize, respOpts) {
			now := time.Now()
			ttl := cache.TTLFor(respHeader, targetURL, p.cfg.Cache.DefaultTTLSeconds, now)
			if ttl > 0 {
				key := cache.BuildKey(r.Method, targetURL, r.Header)
				bodyCopy := make([]byte, len(relResp.Body))
				copy(bodyCopy, relResp.Body)
				entry := &cache.Entry{
					Method:       r.Method,
					URL:          targetURL,
					StatusCode:   relResp.StatusCode,
					Header:       cache.CopyHeader(respHeader),
					Body:         bodyCopy,
					StoredAt:     now,
					ExpiresAt:    now.Add(ttl),
					ETag:         respHeader.Get("ETag"),
					LastModified: respHeader.Get("Last-Modified"),
				}
				p.cache.Put(key, entry)
			}
		}
	}

	// ── Write response to client ──────────────────────────────────────────────
	for k, vals := range relResp.Headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if p.cache != nil {
		w.Header().Set("X-Chunkbridge-Cache", "MISS")
	}
	w.WriteHeader(relResp.StatusCode)
	if len(relResp.Body) > 0 {
		_, _ = io.Copy(w, bytes.NewReader(relResp.Body))
	}
	p.metrics.ProxyResponses.Add(1)
}

// requestTimeout returns the per-request relay timeout derived from config.
// Falls back to 30 s when the config value is zero (e.g. in tests or selftest).
func (p *HTTPProxy) requestTimeout() time.Duration {
	if ms := p.cfg.Proxy.RequestTimeoutMs; ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 30 * time.Second
}
