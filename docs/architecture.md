# Architecture

## Overview

```
┌──────────────────────────────────────────────────────────────┐
│  Agent Process (client side)                                 │
│                                                              │
│  ┌─────────┐     ┌──────────────┐     ┌───────────────────┐ │
│  │  HTTP   │────►│  HTTPProxy   │────►│  relay.Session    │ │
│  │  Client │     │  (ServeHTTP) │     │  (send/receive)   │ │
│  └─────────┘     └──────────────┘     └────────┬──────────┘ │
│                                                │             │
└────────────────────────────────────────────────┼─────────────┘
                                                 │ transport.Transport
                                                 │ (MemoryTransport / MaxTransport)
┌────────────────────────────────────────────────┼─────────────┐
│  Exit Process (exit side)                      │             │
│                                                ▼             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  HTTPExecutor.Run()                                   │  │
│  │  ┌──────────────┐   ┌────────────┐   ┌─────────────┐ │  │
│  │  │  Reassembler │──►│  Policy    │──►│  http.Client│ │  │
│  │  └──────────────┘   └────────────┘   └─────────────┘ │  │
│  └───────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

## Packages

| Package | Responsibility |
|---------|---------------|
| `cmd/chunkbridge` | CLI entry point (`client`, `exit`, `selftest`, `version`) |
| `internal/config` | Config structs, YAML loader, defaults |
| `internal/protocol` | Frame types, encode/decode, chunking, reassembly, ACK |
| `internal/crypto` | XChaCha20-Poly1305 AEAD, Argon2id key derivation |
| `internal/compress` | gzip compress/decompress |
| `internal/transport` | Transport interface, MemoryTransport, MaxTransport skeleton |
| `internal/relay` | Session layer: request/response correlation over transport |
| `internal/proxy` | HTTP proxy server, delegates to relay.Session; optional in-memory cache |
| `internal/exit` | HTTP executor, receives relay requests, makes outbound calls |
| `internal/policy` | Domain allowlist, port block, private-IP block, response limits |
| `internal/ratelimit` | Token-bucket and adaptive rate limiter |
| `internal/cache` | Conservative in-memory LRU response cache (client-side only) |
| `internal/observability` | Structured logger (slog), atomic metrics |

## Data Flow (request)

1. HTTP client → `HTTPProxy.ServeHTTP`
2. **Cache lookup** (if `cache.enabled: true`): if the request is a safe GET/HEAD with no `Authorization`/`Cookie`/`no-cache`, and a fresh entry exists, the response is served from cache (`X-Chunkbridge-Cache: HIT`) and no relay traffic is generated.
3. Proxy serialises `relayRequest{Method, URL, Headers, Body}` → JSON
4. JSON wrapped in `protocol.Frame{Type=DATA, RequestID=uuid}`
5. `relay.Session` chunks the frame if payload > `MaxPayloadBytes` (1600 B)
6. Each chunk is JSON-marshalled, gzip-compressed, XChaCha20-Poly1305-encrypted (AAD = sessionID|seqNum), base64-encoded
7. Each chunk is formatted as `CB1|D|<sessionID>|<seqNum>|<b64>` and sent via `transport.Transport`
8. Exit receives text messages, decodes each frame
9. `protocol.Reassembler` collects chunks; on completion, deserialises `relayRequest`
10. `policy.Policy.CheckRequest` validates URL
11. `http.Client` makes outbound request
12. Response is serialised, chunked, encrypted, sent back the same way
13. Proxy `relay.Session` reassembles response, writes to HTTP response writer
14. **Cache store** (MISS path): if the response is cacheable (status is in the allowed set, no `Set-Cookie`/`private`/`no-store`, `Vary` field is only `Accept-Encoding`, TTL > 0) the response is stored under `method + URL + Accept-Encoding` (`X-Chunkbridge-Cache: MISS` added to client response).

## Encryption Details

```
plaintext  = json.Marshal(Frame)
compressed = gzip.Compress(plaintext)
aad        = []byte(sessionID + "|" + seqNum)
ciphertext = XChaCha20-Poly1305.Seal(nonce, compressed, aad)
encoded    = base64.StdEncoding(nonce || ciphertext)
message    = "CB1|D|" + sessionID + "|" + seqNum + "|" + encoded
```

## Request Lifecycle and Concurrency

### Bounded pending request map

`relay.Session` maintains a `pending` map (requestID → response channel). Each call to `SendRequest` inserts a channel and removes it on return, regardless of how the call exits (success, timeout, context cancellation, send error).

Without a limit, a slow exit node or adversarial conditions could cause the pending map to grow without bound. To prevent this:

- **`proxy.max_concurrent_requests`** (default: **64**) caps the number of simultaneous in-flight requests per session.
- When the limit is reached, `SendRequest` returns immediately with `relay: too many concurrent requests`.
- The proxy maps this to **HTTP 429 Too Many Requests** to the local HTTP client.
- Late responses that arrive after a request has timed out or been cancelled are silently dropped; they do not panic or corrupt state.

This is **not** full flow-control or a sliding window. It is a simple head-of-line protection against unbounded memory growth.

### Per-request timeout

`proxy.request_timeout_ms` (default: **30000 ms**) is used by the proxy as the deadline for each `SendRequest`. A request that receives no response within this window is abandoned with `relay: timeout ...`, and the proxy returns **HTTP 502 Bad Gateway**.

### What this PR does NOT implement

- ACK-based reliable delivery
- Sliding window (WINDOW frames)
- Retry / resend on failure
- Priority queues (control vs. data)
- Back-pressure from the exit node to the client

## Structured Relay Errors (FrameERROR)

When the exit node cannot fulfil a request, it sends a `FrameERROR` frame (type 9) back to the proxy using the same `RequestID`. The frame payload is a JSON `ErrorPayload` with three fields: `code`, `http_status`, and `message`.

`relay.Session.SendRequest` decodes the error and returns a `*relay.RelayError` to the caller. The proxy maps `RelayError.HTTPStatus` directly to the HTTP response:

| Error code | HTTP status | Condition |
|-----------|-------------|-----------|
| `policy_denied` | 403 | IP/domain/port blocked at exit |
| `bad_request` | 400 | Malformed relay request |
| `upstream_unavailable` | 502 | Connection refused or DNS failure |
| `upstream_timeout` | 504 | Exit's HTTP client timed out |
| `response_too_large` | 502 | Response exceeded `MaxResponseBytes` |
| `internal_error` | 502 | Unexpected exit-side failure |

Error messages are sanitised and do not include request URLs, headers, or upstream bodies.

Late `FrameERROR` frames that arrive after the request has already timed out or been cancelled are silently discarded by `dispatch()` — they do not panic or corrupt state.

## Graceful Shutdown

Both `client` and `exit` CLI commands handle `SIGINT` / `SIGTERM`.

**Client mode:** `signal.NotifyContext` cancels the signal context. The main goroutine calls `proxy.Shutdown(ctx)` with a 10-second deadline, which delegates to `http.Server.Shutdown`. In-flight requests complete or are abandoned before the process exits. `Serve` returns `http.ErrServerClosed`, which is treated as a normal exit.

**Exit mode:** The signal context is passed directly to `executor.Run`. When cancelled, `Run` returns `context.Canceled`, which is treated as a clean stop (no error log, no `os.Exit(1)`).

Shutdown is bounded. The client shutdown timeout is 10 seconds. There is no infinite hang.
