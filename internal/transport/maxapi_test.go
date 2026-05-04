package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/transport"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// newTestTransport creates a MaxTransport pointed at srv with a fixed token.
func newTestTransport(t *testing.T, srv *httptest.Server, opts ...func(*transport.MaxTransportConfig)) *transport.MaxTransport {
	t.Helper()
	t.Setenv("TEST_MAX_TOKEN", "secret-token")
	cfg := transport.MaxTransportConfig{
		BaseURL:        srv.URL,
		TokenEnv:       "TEST_MAX_TOKEN",
		PeerChatID:     "chat-99",
		FromHandle:     "self",
		PollIntervalMs: 10, // fast for tests
		PollTimeoutSec: 1,
		SafeChars:      200,
	}
	for _, o := range opts {
		o(&cfg)
	}
	mt, err := transport.NewMaxTransport(cfg)
	if err != nil {
		t.Fatalf("NewMaxTransport: %v", err)
	}
	t.Cleanup(func() { mt.Close() })
	return mt
}

// sendResponse writes a JSON body and status to w.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// pollBody builds a maxPollResponse JSON body with the given messages.
// It uses the same shape as the internal struct but defined inline to avoid
// exposing unexported types.
type pollMsg struct {
	MessageID string `json:"message_id"`
	From      string `json:"from"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}
type pollBody struct {
	Messages []pollMsg `json:"messages"`
}

// ─── Send tests ───────────────────────────────────────────────────────────────

func TestMaxTransportSendSuccess(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotContentType string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		writeJSON(w, http.StatusOK, map[string]string{"message_id": "msg-1"})
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	err := mt.Send(context.Background(), transport.Message{Text: "hello-cb1"})
	if err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %q", gotMethod)
	}
	if gotPath != "/messages" {
		t.Errorf("path: want /messages, got %q", gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization: want %q, got %q", "Bearer secret-token", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("Content-Type: want application/json, got %q", gotContentType)
	}
	if gotBody["chat_id"] != "chat-99" {
		t.Errorf("body.chat_id: want %q, got %q", "chat-99", gotBody["chat_id"])
	}
	if gotBody["text"] != "hello-cb1" {
		t.Errorf("body.text: want %q, got %q", "hello-cb1", gotBody["text"])
	}
}

func TestMaxTransportSendRejectsOversizedMessage(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv, func(c *transport.MaxTransportConfig) {
		c.SafeChars = 5
	})

	err := mt.Send(context.Background(), transport.Message{Text: "this is longer than five"})
	if err == nil {
		t.Fatal("expected error for oversized message, got nil")
	}
	if requestMade {
		t.Error("HTTP request was made despite message exceeding safe limit")
	}
	if !strings.Contains(err.Error(), "safe limit") {
		t.Errorf("error should mention safe limit, got: %v", err)
	}
}

func TestMaxTransportSendRejectsEmptyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP request made for empty message")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	err := mt.Send(context.Background(), transport.Message{Text: ""})
	if err == nil {
		t.Fatal("expected error for empty message, got nil")
	}
}

func TestMaxTransportSendAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	err := mt.Send(context.Background(), transport.Message{Text: "test"})
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
	if !strings.Contains(err.Error(), "authentication error") {
		t.Errorf("error should mention authentication, got: %v", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should include status code 401, got: %v", err)
	}
}

func TestMaxTransportSendRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	on429Called := false
	mt := newTestTransport(t, srv)
	mt.WithOn429(func() { on429Called = true })

	err := mt.Send(context.Background(), transport.Message{Text: "test"})
	if err == nil {
		t.Fatal("expected error for 429 response, got nil")
	}

	var rlErr *transport.RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *RateLimitError, got %T: %v", err, err)
	}
	if rlErr.RetryAfter != 3*time.Second {
		t.Errorf("RetryAfter: want 3s, got %s", rlErr.RetryAfter)
	}
	if !on429Called {
		t.Error("On429 callback was not called on HTTP 429")
	}
}

func TestMaxTransportSendServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	err := mt.Send(context.Background(), transport.Message{Text: "test"})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should include status code 500, got: %v", err)
	}
}

// ─── Receive tests ────────────────────────────────────────────────────────────

func TestMaxTransportReceiveDeliversTextMessage(t *testing.T) {
	ts := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	messages := []pollMsg{
		{MessageID: "m1", From: "peer", Text: "CB1|D|…", CreatedAt: ts.Format(time.RFC3339)},
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages/poll" {
			t.Errorf("unexpected poll path: %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("missing/wrong Authorization header: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("chat_id") != "chat-99" {
			t.Errorf("poll missing chat_id: %q", r.URL.Query().Get("chat_id"))
		}
		callCount++
		// Return messages on first call, empty thereafter.
		if callCount == 1 {
			writeJSON(w, http.StatusOK, pollBody{Messages: messages})
		} else {
			writeJSON(w, http.StatusOK, pollBody{Messages: nil})
		}
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := mt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before delivering message")
		}
		if msg.ID != "m1" {
			t.Errorf("ID: want %q, got %q", "m1", msg.ID)
		}
		if msg.From != "peer" {
			t.Errorf("From: want %q, got %q", "peer", msg.From)
		}
		if msg.Text != "CB1|D|…" {
			t.Errorf("Text: want %q, got %q", "CB1|D|…", msg.Text)
		}
		if !msg.CreatedAt.Equal(ts) {
			t.Errorf("CreatedAt: want %v, got %v", ts, msg.CreatedAt)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for message from Receive channel")
	}
}

func TestMaxTransportReceiveDeduplicatesMessages(t *testing.T) {
	msg := pollMsg{MessageID: "dup-1", From: "peer", Text: "hello"}

	// Respond with the same message on every poll call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, pollBody{Messages: []pollMsg{msg}})
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ch, err := mt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// Collect messages until context expires.
	count := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				goto done
			}
			count++
		case <-ctx.Done():
			goto done
		}
	}
done:
	if count != 1 {
		t.Errorf("expected exactly 1 delivery for duplicated message ID, got %d", count)
	}
}

func TestMaxTransportReceiveStopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, pollBody{Messages: nil})
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := mt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	cancel()

	// Channel must close promptly after cancellation.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed cleanly
			}
		case <-deadline:
			t.Fatal("poll goroutine did not exit within 2s after context cancel")
		}
	}
}

func TestMaxTransportReceiveIgnoresSelfMessages(t *testing.T) {
	messages := []pollMsg{
		{MessageID: "m1", From: "self", Text: "echo from self"},
		{MessageID: "m2", From: "peer", Text: "real message"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, pollBody{Messages: messages})
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv, func(c *transport.MaxTransportConfig) {
		c.FromHandle = "self"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := mt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before delivering peer message")
		}
		if msg.From == "self" {
			t.Errorf("self-message was delivered; expected it to be filtered")
		}
		if msg.ID != "m2" {
			t.Errorf("expected peer message m2, got %q", msg.ID)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for peer message")
	}
}

func TestMaxTransportReceiveBacksOffOn429(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(w, http.StatusOK, pollBody{Messages: []pollMsg{
			{MessageID: "m1", From: "peer", Text: "after-backoff"},
		}})
	}))
	defer srv.Close()

	on429Called := false
	mt := newTestTransport(t, srv)
	mt.WithOn429(func() { on429Called = true })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := mt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before delivering message")
		}
		if msg.Text != "after-backoff" {
			t.Errorf("unexpected message text: %q", msg.Text)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for message after 429 backoff")
	}

	if !on429Called {
		t.Error("On429 callback was not called on receive 429")
	}
}

// ─── Config validation tests ──────────────────────────────────────────────────

// These tests are in the config package; see internal/config/config_max_test.go.
// The transport package tests only cover transport construction.

func TestNewMaxTransportMissingToken(t *testing.T) {
	// Ensure the env var is unset.
	t.Setenv("MISSING_TOKEN_ENV", "")
	cfg := transport.MaxTransportConfig{
		BaseURL:    "http://example.com",
		TokenEnv:   "MISSING_TOKEN_ENV",
		PeerChatID: "chat-1",
	}
	_, err := transport.NewMaxTransport(cfg)
	if err == nil {
		t.Fatal("expected error for missing token env var, got nil")
	}
	if !strings.Contains(err.Error(), "MISSING_TOKEN_ENV") {
		t.Errorf("error should name the missing env var, got: %v", err)
	}
}

func TestRateLimitErrorMessage(t *testing.T) {
	err := &transport.RateLimitError{RetryAfter: 5 * time.Second}
	if !strings.Contains(err.Error(), "5s") {
		t.Errorf("RateLimitError.Error() should include duration, got: %q", err.Error())
	}

	errNoRA := &transport.RateLimitError{}
	if strings.Contains(errNoRA.Error(), "retry after") {
		t.Errorf("RateLimitError without RetryAfter should not mention duration, got: %q", errNoRA.Error())
	}
}

func TestMaxTransportSendForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	err := mt.Send(context.Background(), transport.Message{Text: "test"})
	if err == nil {
		t.Fatal("expected error for 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error should include 403, got: %v", err)
	}
}

func TestMaxTransportSend413(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))
	defer srv.Close()

	mt := newTestTransport(t, srv)
	err := mt.Send(context.Background(), transport.Message{Text: "test"})
	if err == nil {
		t.Fatal("expected error for 413 response, got nil")
	}
	if !strings.Contains(err.Error(), "413") {
		t.Errorf("error should include 413, got: %v", err)
	}
}

func TestMaxTransportCloseIsIdempotent(t *testing.T) {
	t.Setenv("TEST_MAX_TOKEN", "tok")
	cfg := transport.MaxTransportConfig{
		BaseURL:    "http://localhost:9",
		TokenEnv:   "TEST_MAX_TOKEN",
		PeerChatID: "c",
	}
	mt, err := transport.NewMaxTransport(cfg)
	if err != nil {
		t.Fatalf("NewMaxTransport: %v", err)
	}
	if err := mt.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := mt.Close(); err != nil {
		t.Errorf("second Close (idempotent): %v", err)
	}
}
