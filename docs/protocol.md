# Protocol

## Message Format

All chunkbridge messages sent over a transport take the following text form:

```
CB1|D|<session_id>|<seq_num>|<base64_encrypted_data>
```

| Field | Description |
|-------|-------------|
| `CB1` | Protocol version prefix (always `CB1`) |
| `D` | Message type (`D` = data) |
| `<session_id>` | Identifies the client session |
| `<seq_num>` | Monotonically increasing sequence number (used as AAD) |
| `<base64_encrypted_data>` | Standard base64-encoded ciphertext (see below) |

Character limits: safe ≤ 3 600 chars total; base64 data ≤ 3 400 chars.

## Frame Types

| Constant | Value | Description | Implemented |
|----------|-------|-------------|-------------|
| `FrameHELLO` | 1 | Handshake | No |
| `FrameOPEN` | 2 | Open stream | No |
| `FrameDATA` | 3 | Application data | **Yes** |
| `FrameACK` | 4 | Acknowledgement | No |
| `FrameWINDOW` | 5 | Window update | No |
| `FrameCLOSE` | 6 | Close stream | No |
| `FramePING` | 7 | Keep-alive ping | No |
| `FramePONG` | 8 | Ping reply | No |
| `FrameERROR` | 9 | Error notification | **Yes** |

## Frame Struct (JSON)

```json
{
  "v":     1,
  "t":     3,
  "sid":   "proxy-1234567890",
  "rid":   "a1b2c3d4e5f60708",
  "seq":   7,
  "total": 3,
  "idx":   0,
  "payload": "<base64>"
}
```

## Chunking

When a frame's payload exceeds `MaxPayloadBytes` (1 600 bytes), it is split into multiple frames with the same `SessionID` and `RequestID` but different `ChunkIndex` values (0 … `TotalChunks-1`). The receiver uses `Reassembler` to collect and reassemble them.

## Encryption Pipeline

```
Frame JSON  ──gzip──►  compressed  ──XChaCha20-Poly1305──►  ciphertext
AAD = sessionID + "|" + seqNum
nonce = random 24 bytes (prepended to ciphertext)
wire = base64(nonce || ciphertext)
```

Decryption is the exact reverse.

## Request/Response Serialisation

HTTP requests and responses are serialised to JSON inside the `Frame.Payload`:

```go
// Request (FrameDATA, proxy → exit)
type relayRequest struct {
    Method  string
    URL     string              // absolute URL
    Headers map[string][]string
    Body    []byte
}

// Response (FrameDATA, exit → proxy)
type relayResponse struct {
    StatusCode int
    Headers    map[string][]string
    Body       []byte
}
```

## FrameERROR Payload

When the exit node cannot fulfil a request, it sends a `FrameERROR` frame back with the same `RequestID`. The payload is:

```json
{
  "code":        "policy_denied",
  "http_status": 403,
  "message":     "request denied by policy"
}
```

### Error Codes

| Code | Default HTTP Status | Condition |
|------|---------------------|-----------|
| `policy_denied` | 403 | Domain/IP/port blocked by exit policy |
| `bad_request` | 400 | Malformed relay request or invalid URL |
| `upstream_unavailable` | 502 | Cannot connect to upstream server |
| `upstream_timeout` | 504 | Upstream did not respond within exit timeout |
| `response_too_large` | 502 | Upstream response exceeds `MaxResponseBytes` |
| `internal_error` | 502 | Unexpected internal error on the exit node |

The proxy maps `FrameERROR.http_status` directly to the HTTP response returned to the local client. Error messages are sanitised and do not include request URLs, headers, or upstream response bodies.

### Not Implemented

The following frame types are defined but not implemented:

- `FrameACK` — reliable delivery / acknowledgement
- `FrameWINDOW` — sliding-window flow control
- `FrameCLOSE` — explicit stream close
- `FrameHELLO` / `FrameOPEN` / `FramePING` / `FramePONG` — handshake and keep-alive

There is no retry, resend, or ordering guarantee. `FrameDATA` delivers best-effort.
