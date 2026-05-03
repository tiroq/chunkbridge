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
| `internal/proxy` | HTTP proxy server, delegates to relay.Session |
| `internal/exit` | HTTP executor, receives relay requests, makes outbound calls |
| `internal/policy` | Domain allowlist, port block, private-IP block, response limits |
| `internal/ratelimit` | Token-bucket and adaptive rate limiter |
| `internal/observability` | Structured logger (slog), atomic metrics |

## Data Flow (request)

1. HTTP client → `HTTPProxy.ServeHTTP`
2. Proxy serialises `relayRequest{Method, URL, Headers, Body}` → JSON
3. JSON wrapped in `protocol.Frame{Type=DATA, RequestID=uuid}`
4. `relay.Session` chunks the frame if payload > `MaxPayloadBytes` (1600 B)
5. Each chunk is JSON-marshalled, gzip-compressed, XChaCha20-Poly1305-encrypted (AAD = sessionID|seqNum), base64-encoded
6. Each chunk is formatted as `CB1|D|<sessionID>|<seqNum>|<b64>` and sent via `transport.Transport`
7. Exit receives text messages, decodes each frame
8. `protocol.Reassembler` collects chunks; on completion, deserialises `relayRequest`
9. `policy.Policy.CheckRequest` validates URL
10. `http.Client` makes outbound request
11. Response is serialised, chunked, encrypted, sent back the same way
12. Proxy `relay.Session` reassembles response, writes to HTTP response writer

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
