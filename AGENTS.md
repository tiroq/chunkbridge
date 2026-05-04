# chunkbridge — Agent Instructions

chunkbridge tunnels arbitrary HTTP traffic through a text-only messaging channel via a split-proxy architecture: a **client** (HTTP proxy) serialises requests into encrypted CB/1 chunks, and an **exit node** reassembles and executes them.

## Essential Reading

| Document | When to read |
|----------|-------------|
| [docs/architecture.md](docs/architecture.md) | Before touching proxy, exit, relay, or transport |
| [docs/protocol.md](docs/protocol.md) | Before changing frame encoding, chunking, or wire format |
| [docs/security.md](docs/security.md) | Before changing crypto, policy, or key derivation |
| [docs/max-transport.md](docs/max-transport.md) | Before implementing MaxTransport |

## Build & Test

```bash
task build              # go build -o bin/chunkbridge ./cmd/chunkbridge
task test               # go test ./... -timeout 120s
task test-unit          # go test ./internal/... -timeout 60s
task test-integration   # go test ./tests/integration/... -timeout 60s -v
task vet                # go vet ./...
CHUNKBRIDGE_SHARED_KEY=testpassphrase go run ./cmd/chunkbridge selftest
```

No linter is configured; `go vet` is the only static analysis step.

## Package Map

| Package | Role |
|---------|------|
| `cmd/chunkbridge` | CLI: wires key derivation, transport, proxy/exit together |
| `internal/config` | YAML config loader; `DefaultClientConfig()` / `DefaultExitConfig()` |
| `internal/maxtransport` | MAX-specific `Transport` adapter (`MaxTransport`, `MaxTransportConfig`, `RateLimitError`) |
| `internal/proxy` | `HTTPProxy` — local HTTP proxy server; calls `relay.Session.SendRequest` |
| `internal/exit` | `HTTPExecutor` — reads transport, reassembles, dispatches outbound HTTP |
| `internal/policy` | `Policy.CheckRequest`: scheme → port → private-IP → domain allow-list |
| `internal/observability` | `Logger` (slog) and `Metrics` (`atomic.Int64` counters) |
| `internal/cache` | Conservative in-memory LRU response cache (client-side only) |
| **relaykit** (external) | Generic relay library — see below |

### relaykit packages (consumed via `github.com/tiroq/relaykit`)

| Package | Role |
|---------|------|
| `pkg/crypto` | `Encrypt`/`Decrypt` (XChaCha20-Poly1305), `DeriveKey`/`GenerateSalt` (Argon2id) |
| `pkg/compress` | Thin `gzip` wrappers used in the encode/decode pipeline |
| `pkg/protocol` | `Frame`, `EncodeMessage`/`DecodeMessage`, `Chunk`, `Reassembler`, ACK |
| `pkg/transport` | `Transport` interface, `MemoryTransport` |
| `pkg/relay` | `Session` — manages seqNum, pending map, reassembler; owns send/receive loop |
| `pkg/ratelimit` | Token-bucket + adaptive limiter |

## Critical Constraints

**`relay.Session.Start` must run before `SendRequest`.** `Start` is the goroutine that dispatches responses to pending callers. Without it, every `SendRequest` times out. `HTTPProxy.Serve` starts it correctly; any new integration must too.

**`Chunk()` does not assign `SeqNum`.** Both `relay.Session.sendFrame` and `exit.HTTPExecutor.sendFrame` atomically increment `seqNum` per chunk after chunking. Forgetting this causes AAD collisions and non-deterministic decryption failures.

**Salt must be exactly 16 bytes.** `DeriveKey` returns an error on wrong size; `main.go` exits. The selftest hardcodes a 16-byte salt.

**`relayRequest`/`relayResponse` are duplicated** — identical private structs exist in both `internal/proxy` and `internal/exit`. Changing one requires changing the other.

**`exit.HTTPExecutor` does not use `relay.Session`** — it talks directly to the transport and maintains its own `seqNum` and `Reassembler`.

**HTTPS CONNECT is intentionally unsupported** — `HTTPProxy` returns `501` for `CONNECT`. Do not add CONNECT tunnelling without a design review.

**Policy is applied independently at both ends.** Default client config has `BlockPrivateRanges: false`; default exit has `BlockPrivateRanges: true`. Integration tests must set `exitCfg.Policy.BlockPrivateRanges = false` to allow `httptest.NewServer` (localhost) targets.

**Wire-format character budget is not enforced in `EncodeMessage`.** The 3 600/3 400-char limits live in `config.MessageConfig`. `MaxPayloadBytes = 1600` in the chunker is the practical guard.

## Wire Format

```
CB1|D|<session_id>|<seq_num>|<base64(nonce||ciphertext)>
```

Encode pipeline: `Frame → json.Marshal → gzip → XChaCha20-Poly1305(AAD=sessionID+"|"+seqNum) → prepend 24-byte nonce → base64`

Decode is the exact reverse. See [docs/protocol.md](docs/protocol.md).

## Conventions

- **Errors**: `fmt.Errorf("package: context: %w", err)` — always prefix with the package name.
- **Sentinel errors**: `var ErrFoo = fmt.Errorf(...)`, except `ErrClosed` which uses an unexported string type to be unspoofable.
- **Session IDs**: nanosecond timestamp (`"proxy-%d"` / `"exit-%d"`), not UUID.
- **Request IDs**: 8 random bytes, hex-encoded (16 chars).
- **Metrics**: `atomic.Int64` counters only; no gauges or histograms.
- **Tests**: standard `testing.T`, table-driven; no external test framework.

## Adding a New Transport

1. Implement `transport.Transport` from `github.com/tiroq/relaykit/pkg/transport` (`Send`, `Receive`, `Close`).
2. Place the adapter in `internal/maxtransport` (MAX-specific) or create a new `internal/<name>transport` package.
3. Add a new case to `buildTransport` in `cmd/chunkbridge/main.go`.
4. Add config fields under `TransportConfig` in `internal/config/config.go`.
5. Read [docs/max-transport.md](docs/max-transport.md) for Max.ai-specific constraints.
