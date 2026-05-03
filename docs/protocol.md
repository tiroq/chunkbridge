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

| Constant | Value | Description |
|----------|-------|-------------|
| `FrameHELLO` | 1 | Handshake |
| `FrameOPEN` | 2 | Open stream |
| `FrameDATA` | 3 | Application data |
| `FrameACK` | 4 | Acknowledgement |
| `FrameWINDOW` | 5 | Window update |
| `FrameCLOSE` | 6 | Close stream |
| `FramePING` | 7 | Keep-alive ping |
| `FramePONG` | 8 | Ping reply |
| `FrameERROR` | 9 | Error notification |

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
// Request
type relayRequest struct {
    Method  string
    URL     string              // absolute URL
    Headers map[string][]string
    Body    []byte
}

// Response
type relayResponse struct {
    StatusCode int
    Headers    map[string][]string
    Body       []byte
}
```
