# Max Transport

`internal/transport/maxapi.go` implements `MaxTransport`, an HTTP-based adapter
for the Max.ai Bot API that satisfies the `transport.Transport` interface.

## Current Status

`MaxTransport` is **functional against a mocked HTTP API** and has been tested
with `httptest.Server`. It has **not been validated against the live Max.ai
production endpoint** — the exact endpoint URLs, authentication scheme, and
response shapes below are assumed based on available documentation and may
require adjustment once live API access is available.

## Configuration

```yaml
transport:
  type: max
  max:
    token_env: MAX_API_TOKEN      # env var holding the bearer token (required)
    base_url: https://api.max.example.com/v1  # API root URL (required)
    peer_chat_id: "chat-123"      # chat ID of the remote endpoint (required)
    from_handle: "@my-agent"      # this endpoint's handle; used to filter echoes
    poll_ms: 1000                 # ms between empty-response polls (default: 1000)
    poll_timeout_sec: 20          # server-side long-poll timeout param (default: 20)
    dedupe_max_ids: 4096          # deduplication window capacity (default: 4096)
```

`Config.Validate()` enforces the following when `transport.type` is `"max"`:
- `transport.max.base_url` must be non-empty.
- `transport.max.token_env` must be non-empty.
- `transport.max.peer_chat_id` must be non-empty.
- `transport.max.dedupe_max_ids` must be > 0.

The token value is **not** validated at config time; it is read from the
environment at startup and an error is returned immediately if the env var is
unset or empty.

## Assumed API JSON Shapes

> **Important:** These shapes are assumed based on available documentation.
> They are not confirmed by a live API test. Adjust the internal structs in
> `maxapi.go` as needed once live endpoint specs are available.

### Send — POST `<base_url>/messages`

Request body:
```json
{
  "chat_id": "<peer_chat_id>",
  "text": "<encoded chunkbridge payload>"
}
```

Successful response (2xx):
```json
{
  "message_id": "<string>",
  "created_at": "<RFC 3339 timestamp>"
}
```

### Receive — GET `<base_url>/messages/poll`

Query parameters:
| Parameter | Description |
|-----------|-------------|
| `chat_id` | the peer chat ID |
| `timeout` | server-side long-poll timeout in seconds |
| `after_id` | last seen message ID for pagination (omitted on first call) |

Response body:
```json
{
  "messages": [
    {
      "message_id": "<string>",
      "from": "<sender handle>",
      "text": "<message text>",
      "created_at": "<RFC 3339 timestamp>"
    }
  ]
}
```

An empty `messages` array means no new messages during the poll window.

## Send Behaviour

- Rejects empty message text with an immediate error (no HTTP call).
- Rejects messages longer than `rate_limits.message.safe_chars` runes (if > 0) with an immediate error (no HTTP call). Does **not** silently truncate.
- Sets `Authorization: Bearer <token>` and `Content-Type: application/json`.
- Returns a typed `*RateLimitError` on HTTP 429, including the parsed
  `Retry-After` duration if the header is present.
- Returns a clear error on 401/403 ("authentication error"), 404 ("endpoint not
  found; check base_url and peer_chat_id"), 413 ("message too large"), and any
  other non-2xx status.
- All errors are prefixed with `transport: max:`.
- The bearer token is never included in any error message.

## Receive / Polling Behaviour

- Starts a background goroutine that calls `GET /messages/poll` in a loop.
- **`Receive` must be called at most once** per `MaxTransport` instance. A second call returns `fmt.Errorf("transport: max: receive already started")` without starting a goroutine.
- Delivers messages on a buffered channel (capacity 256).
- Deduplicates messages by `message_id` using a **bounded FIFO window** of
  `dedupe_max_ids` entries (default 4096). When the window is full the oldest
  ID is evicted; a message whose ID was evicted **may be re-delivered**. This
  is intentional and prevents unbounded memory growth in long-running deployments.
- Filters echo: messages whose `from` field matches `from_handle` are dropped.
- Parses `created_at` as RFC 3339; falls back to `time.Now()` if absent or
  malformed.
- **Stops cleanly on context cancellation or `Close()`**, including unblocking
  any in-flight HTTP request. `Close()` is idempotent.
- On HTTP 429 during polling: calls the `On429` callback (if registered), waits
  for the parsed `Retry-After` duration (or one poll interval if absent), then
  retries.
- On auth errors (401/403) during polling: stops the poll loop immediately
  (non-retryable).
- On network / 5xx errors: waits one poll interval then retries.

## 429 Rate-Limit Feedback

`WithOn429(fn func()) *MaxTransport` registers a callback invoked on every HTTP
429 response (both send and receive paths). Wire it to
`AdaptiveRateLimiter.On429()` to halve the data send rate:

```go
lim := ratelimit.NewAdaptiveRateLimiter(...)
mt.WithOn429(lim.On429)
```

The `Retry-After` header value is parsed (integer seconds or HTTP-date) and
returned in the `*RateLimitError`. The transport does **not** automatically
sleep and retry sends — the caller is responsible for honouring `RetryAfter`.

## Security

- The bearer token is read once from the environment at construction time.
- The token is never logged or included in error messages.
- TLS certificate verification is always enabled (`InsecureSkipVerify` is
  not set).

## Test Coverage

All tests in `internal/transport/maxapi_test.go` use `httptest.Server`.
No real MAX API credentials or network access are required.

| Test | Covers |
|------|—-----|
| `TestMaxTransportSendSuccess` | Method, path, Authorization header, Content-Type, JSON body, peer_chat_id |
| `TestMaxTransportSendRejectsOversizedMessage` | Pre-flight size check; no HTTP call made |
| `TestMaxTransportSendRejectsEmptyMessage` | Empty text rejection |
| `TestMaxTransportSendAuthError` | 401 → clear auth error |
| `TestMaxTransportSendForbidden` | 403 → clear auth error |
| `TestMaxTransportSend413` | 413 → clear server-rejected-too-large error |
| `TestMaxTransportSendRateLimited` | 429 + Retry-After → `*RateLimitError`; On429 callback called |
| `TestMaxTransportSendServerError` | 500 → clear error with status code |
| `TestMaxTransportReceiveDeliversTextMessage` | Poll path, Authorization, chat_id, message delivery, timestamp parsing |
| `TestMaxTransportReceiveDeduplicatesMessages` | Same message_id returned twice → delivered once |
| `TestMaxTransportReceiveStopsOnContextCancel` | Goroutine exits; channel closed |
| `TestMaxTransportReceiveIgnoresSelfMessages` | from_handle filtering |
| `TestMaxTransportReceiveBacksOffOn429` | 429 in poll loop → On429 callback; resumes after backoff |
| `TestNewMaxTransportMissingToken` | Missing env var → construction error naming the var |
| `TestRateLimitErrorMessage` | `RateLimitError.Error()` format with and without RetryAfter |
| `TestMaxTransportCloseIsIdempotent` | Double `Close()` does not panic |
| `TestMaxTransportReceiveDedupeBounded` | Window eviction: ID re-delivered after eviction from bounded window |
| `TestMaxTransportReceiveRejectsSecondReceive` | Second `Receive()` call returns "receive already started" error |
| `TestMaxTransportCloseUnblocksReceive` | `Close()` cancels in-flight poll request; channel closes within 2 s |
| `TestMaxTransportPollClosesResponseBody` | All HTTP response bodies are closed (tracked via custom RoundTripper) |
| `TestMaxTransportReceiveEmptyPollDoesNotSpin` | Empty poll responses respect poll interval; ≤ 10 requests in 300 ms |

Config validation tests are in `internal/config/config_max_test.go`:

| Test | Covers |
|------|--------|
| `TestValidateMaxMissingBaseURL` | base_url required for type=max |
| `TestValidateMaxMissingTokenEnv` | token_env required for type=max |
| `TestValidateMaxMissingPeerChatID` | peer_chat_id required for type=max |
| `TestValidateMaxValidConfig` | fully populated max config passes |
| `TestValidateMemoryTransportDoesNotRequireMaxFields` | memory transport unaffected |
| `TestValidateMaxInvalidDedupeMaxIDs` | dedupe_max_ids = 0 or negative rejected |

## Remaining Gaps (Live Validation)

The following are **not** validated against the real Max.ai API and may need
adjustment:

| Gap | Notes |
|-----|-------|
| Exact endpoint paths | `/messages` and `/messages/poll` are assumed. |
| Response shape | JSON field names (`message_id`, `from`, `text`, `created_at`) are assumed. |
| Authentication scheme | `Authorization: Bearer` is assumed; the real API may differ. |
| Long-poll timeout parameter | `?timeout=<n>` is assumed. |
| Retry-After full retry loop | `*RateLimitError.RetryAfter` is returned but the transport does not automatically sleep-and-retry on send. |
| Webhook mode | Not implemented. |
| ACK / WINDOW / retry | Protocol-level reliability is not in transport scope. |

