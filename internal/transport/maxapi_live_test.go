//go:build live

// Package transport_test — live MAX API contract validation.
//
// These tests send real HTTP requests to the Max.ai Bot API. They are gated
// behind both the `live` build tag and a required environment variable so they
// never run accidentally in CI or during normal `go test ./...` runs.
//
// Required environment variables:
//
//	CHUNKBRIDGE_LIVE_MAX_TESTS=1
//	CHUNKBRIDGE_MAX_BASE_URL        e.g. https://platform-api.max.ru
//	CHUNKBRIDGE_MAX_TOKEN           bearer token (never commit this)
//	CHUNKBRIDGE_MAX_PEER_CHAT_ID    target chat ID
//
// Optional:
//
//	CHUNKBRIDGE_MAX_POLL_TIMEOUT_SEC  server-side long-poll timeout (default 5)
//	CHUNKBRIDGE_MAX_EXPECT_RECEIVE=1  also validate the receive path
//
// Run with:
//
//	export CHUNKBRIDGE_LIVE_MAX_TESTS=1
//	export CHUNKBRIDGE_MAX_BASE_URL="https://platform-api.max.ru"
//	export CHUNKBRIDGE_MAX_TOKEN="..."
//	export CHUNKBRIDGE_MAX_PEER_CHAT_ID="..."
//	go test -tags=live ./internal/transport -run Live -v -count=1
//
// Or via Taskfile:
//
//	task test-live-max
//
// Safety:
//   - Sends one small diagnostic message per test run.
//   - Does not test rate limits aggressively.
//   - Does not send sensitive data.
//   - The message text includes "live contract test" so it can be distinguished
//     from real traffic in the chat history.
package transport_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/tiroq/chunkbridge/internal/transport"
)

// liveSkipUnless checks that all required env vars for live tests are set.
// If any are missing the test is skipped with a descriptive message.
func liveSkipUnless(t *testing.T) {
	t.Helper()

	if os.Getenv("CHUNKBRIDGE_LIVE_MAX_TESTS") != "1" {
		t.Skip("live MAX tests disabled: set CHUNKBRIDGE_LIVE_MAX_TESTS=1 to enable")
	}
	for _, key := range []string{
		"CHUNKBRIDGE_MAX_BASE_URL",
		"CHUNKBRIDGE_MAX_TOKEN",
		"CHUNKBRIDGE_MAX_PEER_CHAT_ID",
	} {
		if os.Getenv(key) == "" {
			t.Skipf("live MAX tests disabled: %s is not set", key)
		}
	}
}

// newLiveTransport builds a MaxTransport from the live env vars.
func newLiveTransport(t *testing.T) *transport.MaxTransport {
	t.Helper()

	// Map the live env var to a name that NewMaxTransport reads via os.Getenv.
	// We use the real env var name directly as token_env so no extra indirection
	// is needed.
	t.Setenv("CHUNKBRIDGE_MAX_TOKEN", os.Getenv("CHUNKBRIDGE_MAX_TOKEN"))

	pollTimeoutSec := 5
	if s := os.Getenv("CHUNKBRIDGE_MAX_POLL_TIMEOUT_SEC"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			pollTimeoutSec = n
		}
	}

	cfg := transport.MaxTransportConfig{
		BaseURL:        os.Getenv("CHUNKBRIDGE_MAX_BASE_URL"),
		TokenEnv:       "CHUNKBRIDGE_MAX_TOKEN",
		PeerChatID:     os.Getenv("CHUNKBRIDGE_MAX_PEER_CHAT_ID"),
		PollIntervalMs: 500,
		PollTimeoutSec: pollTimeoutSec,
		// No SafeChars limit during live testing — keep it permissive so
		// the send path isn't blocked by the pre-flight check.
		SafeChars: 0,
	}

	mt, err := transport.NewMaxTransport(cfg)
	if err != nil {
		t.Fatalf("NewMaxTransport: %v", err)
	}
	t.Cleanup(func() { mt.Close() })
	return mt
}

// diagnosticText returns a small, clearly-labelled message body that is safe
// to send to any test chat. It includes a timestamp so repeated runs produce
// distinct messages in the chat history.
func diagnosticText() string {
	return fmt.Sprintf("chunkbridge live contract test %s", time.Now().UTC().Format(time.RFC3339))
}

// ─── TestMaxTransportLiveSend ─────────────────────────────────────────────────

// TestMaxTransportLiveSend validates the send path against the real MAX API.
//
// What it checks:
//   - POST /messages returns 2xx.
//   - The assumed JSON request shape is accepted by the server.
//   - The Authorization: Bearer scheme is accepted.
//
// What it does NOT check:
//   - Whether the message is delivered to the chat UI.
//   - Receive/poll path.
func TestMaxTransportLiveSend(t *testing.T) {
	liveSkipUnless(t)

	mt := newLiveTransport(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msg := transport.Message{Text: diagnosticText()}
	if err := mt.Send(ctx, msg); err != nil {
		t.Fatalf("Send failed: %v\n\n"+
			"Possible causes:\n"+
			"  • base_url path is wrong (assumed: /messages)\n"+
			"  • token is expired or invalid\n"+
			"  • peer_chat_id does not exist\n"+
			"  • JSON field names differ from assumed shape\n"+
			"  • Authorization header scheme differs (assumed: Bearer)\n"+
			"See docs/live-max-validation.md for remediation steps.", err)
	}
	t.Logf("Send: OK — message %q delivered to chat %s",
		msg.Text, os.Getenv("CHUNKBRIDGE_MAX_PEER_CHAT_ID"))
}

// ─── TestMaxTransportLiveReceiveOptional ──────────────────────────────────────

// TestMaxTransportLiveReceiveOptional validates the receive/poll path.
// This test is skipped unless CHUNKBRIDGE_MAX_EXPECT_RECEIVE=1 is also set,
// because receiving requires an inbound message from the peer which may not
// arrive during the test window.
//
// What it checks (when enabled):
//   - GET /messages/poll returns a parseable response (2xx, valid JSON).
//   - The assumed poll response shape matches the real API.
//
// What it does NOT do:
//   - Assert that any specific message arrives — it only validates the poll
//     path is reachable and the response parses correctly.
//   - Block for the full poll timeout if an empty response is returned; it
//     treats an empty poll as a successful validation of the schema (the API
//     returned 2xx + valid JSON).
func TestMaxTransportLiveReceiveOptional(t *testing.T) {
	liveSkipUnless(t)
	if os.Getenv("CHUNKBRIDGE_MAX_EXPECT_RECEIVE") != "1" {
		t.Skip("receive validation skipped: set CHUNKBRIDGE_MAX_EXPECT_RECEIVE=1 to enable")
	}

	mt := newLiveTransport(t)

	// Use a short deadline so the test does not block for the full poll window.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ch, err := mt.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v\n\n"+
			"Possible causes:\n"+
			"  • poll endpoint path is wrong (assumed: /messages/poll)\n"+
			"  • chat_id query parameter name differs\n"+
			"  • timeout parameter name differs\n"+
			"See docs/live-max-validation.md for remediation steps.", err)
	}

	t.Log("Receive: poll started — waiting up to 15 s for any message or empty poll response")

	// Drain messages for up to the context deadline, then report what arrived.
	var count int
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				// Channel closed — context deadline reached or Close() called.
				t.Logf("Receive: poll channel closed after receiving %d message(s)", count)
				return
			}
			count++
			t.Logf("Receive: got message #%d — %d rune(s) of text", count, len([]rune(msg.Text)))
		case <-ctx.Done():
			t.Logf("Receive: context deadline — %d message(s) received during window", count)
			// An empty window is not a failure; it means no inbound messages
			// arrived. The important thing is that the poll call did not error.
			return
		}
	}
}
