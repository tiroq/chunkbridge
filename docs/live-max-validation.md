# Live MAX API Contract Validation

## Purpose

This harness validates the assumptions in `internal/transport/maxapi.go`
against the real Max.ai Bot API. It is **not** a load test, a stress test, or
part of normal CI. It exists to discover whether the assumed endpoint paths,
JSON field names, and authentication scheme are correct.

All API contract assumptions are clearly documented in
[docs/max-transport.md](max-transport.md). Until a successful live run is
completed and the results recorded, those assumptions remain unconfirmed.

---

## Safety Warnings

> **Read before running.**

* **Do not use a production chat** that carries real traffic, unless you are
  comfortable seeing `"chunkbridge live contract test <timestamp>"` messages
  appear in that chat's history.
* **Do not run repeatedly** in a tight loop — one run per validation session is
  enough.
* **Do not commit your token.** The token must stay in your shell environment
  only. Never paste it into a YAML config file or source file that could be
  committed.
* **Do not store the token in `.env.live-max`** and commit that file. The
  provided `.env.live-max.example` has an empty `CHUNKBRIDGE_MAX_TOKEN=` line
  intentionally — fill it in your shell, never in a file.
* **CI never uses these tests.** The `live` build tag and the
  `CHUNKBRIDGE_LIVE_MAX_TESTS=1` env var gate ensure the tests are invisible to
  normal `go test ./...` and to the GitHub Actions workflow.

---

## Required Environment Variables

| Variable | Description |
|----------|-------------|
| `CHUNKBRIDGE_LIVE_MAX_TESTS` | Must be `1` to enable. |
| `CHUNKBRIDGE_MAX_BASE_URL` | Root URL of the MAX Bot API. Example: `https://platform-api.max.ru` |
| `CHUNKBRIDGE_MAX_TOKEN` | Bearer token. Read-once at construction; never logged. |
| `CHUNKBRIDGE_MAX_PEER_CHAT_ID` | Chat ID of the target chat for test messages. |

### Optional environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CHUNKBRIDGE_MAX_POLL_TIMEOUT_SEC` | `5` | Server-side long-poll timeout for receive test. |
| `CHUNKBRIDGE_MAX_EXPECT_RECEIVE` | unset | Set to `1` to also run the receive/poll validation test. |

---

## How to Run

```bash
# Set required credentials in your shell (do NOT save to a committed file).
export CHUNKBRIDGE_LIVE_MAX_TESTS=1
export CHUNKBRIDGE_MAX_BASE_URL="https://platform-api.max.ru"
export CHUNKBRIDGE_MAX_TOKEN="your-token-here"
export CHUNKBRIDGE_MAX_PEER_CHAT_ID="your-chat-id"

# Run live send test only (safe default).
task test-live-max

# Also validate the receive/poll path.
CHUNKBRIDGE_MAX_EXPECT_RECEIVE=1 task test-live-max
```

You can also run directly without Task:

```bash
CHUNKBRIDGE_LIVE_MAX_TESTS=1 \
  go test -tags=live ./internal/transport -run Live -v -count=1
```

---

## What the Tests Send

### `TestMaxTransportLiveSend`

Sends a single POST request to `<base_url>/messages` with this body:

```json
{
  "chat_id": "<CHUNKBRIDGE_MAX_PEER_CHAT_ID>",
  "text": "chunkbridge live contract test 2026-05-04T18:00:00Z"
}
```

The timestamp changes every run. The message is clearly labelled so it can be
identified and optionally cleaned up in the chat history.

### `TestMaxTransportLiveReceiveOptional`

Starts a poll loop with a 15-second deadline and logs how many messages (if
any) arrive. Does not assert that a specific message is received — only that
the poll endpoint returns a valid response.

---

## What Successful Output Means

```
=== RUN   TestMaxTransportLiveSend
    maxapi_live_test.go:109: Send: OK — message "chunkbridge live contract test ..." delivered to chat <id>
--- PASS: TestMaxTransportLiveSend (0.42s)
```

A passing `TestMaxTransportLiveSend` means:
- The `POST /messages` endpoint exists at the assumed path.
- The JSON request body shape (`chat_id`, `text`) is accepted.
- `Authorization: Bearer <token>` is the correct auth scheme.
- The server returned a 2xx response.

```
=== RUN   TestMaxTransportLiveReceiveOptional
    maxapi_live_test.go:146: Receive: poll started — waiting up to 15 s ...
    maxapi_live_test.go:165: Receive: context deadline — 0 message(s) received during window
--- PASS: TestMaxTransportLiveReceiveOptional (15.00s)
```

A passing `TestMaxTransportLiveReceiveOptional` with zero messages means:
- The `GET /messages/poll` endpoint exists and returned a valid (possibly
  empty) response.
- The assumed JSON response shape (`{"messages": [...]}`) did not cause a
  parse error.

---

## What Failure Means

### Send fails with a connection or 404 error

The `base_url` or endpoint path is wrong. Check:
- Is `CHUNKBRIDGE_MAX_BASE_URL` set to the correct root URL?
- Does the API use `/messages` or a different path?
- Update `maxapi.go` lines that build `m.baseURL+"/messages"`.

### Send fails with 401 or 403

The token is invalid or expired. Check:
- Is `CHUNKBRIDGE_MAX_TOKEN` set to a valid, non-expired token?
- Does the API use `Authorization: Bearer` or a different header/scheme?
- If the scheme differs, update the `Send` and `pollOnce` methods in
  `maxapi.go`.

### Send fails with 400 or 422

The JSON request body shape is wrong. The assumed shape is:

```json
{ "chat_id": "...", "text": "..." }
```

Compare against the actual API spec and update `maxSendRequest` in `maxapi.go`.

### Receive fails immediately

The poll endpoint path or query parameter names are wrong. The assumed call is:

```
GET <base_url>/messages/poll?chat_id=<id>&timeout=<n>
```

Check the actual API spec and update `pollOnce` in `maxapi.go`.

### Receive succeeds but messages are silently dropped

Check `from_handle`. If your bot's handle/ID matches the `FromHandle` config
field, messages are filtered as self-echoes. Leave `FromHandle` empty during
live validation unless echo filtering is confirmed to be needed.

---

## How to Update MaxTransport After Live Results

1. Identify which assumption failed (endpoint path, JSON shape, auth scheme).
2. Update the relevant struct or method in `internal/transport/maxapi.go`.
3. Update the corresponding mock in `internal/transport/maxapi_test.go` to
   match the confirmed shape.
4. Update the "Assumed API JSON Shapes" section in `docs/max-transport.md`.
5. Re-run `task test` to confirm mocked tests still pass.
6. Re-run `task test-live-max` to confirm the live test passes with the fix.

---

## After Successful Validation

Once live tests pass, update `docs/max-transport.md`:
- Change "assumed" language to "confirmed".
- Record which API version / date was validated.
- Remove "Remaining Gaps" items that are now confirmed.

Consider adding the relevant API response fixture to `maxapi_test.go` so the
mocked test reflects the confirmed shape exactly.
