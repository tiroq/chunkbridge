# chunkbridge Post-PR6 Implementation Audit

**Date:** 2026-05-03  
**Auditor:** automated code + runtime inspection  
**Branch:** main (post-PR6)  
**Baseline:** `docs/implementation-audit.md` (original, pre-PR2)

---

## 1. Executive Summary

**Current status: `MVP_READY` (memory transport) / `PARTIAL_MVP` (production)**

Six PRs have been landed since the original audit. Every gap that was
`MISSING` or `UNSAFE` in the original report targeting the _working_ data path
is now closed. The core relay loop is fully wired: encryption, policy
enforcement, DNS rebinding mitigation, hop-by-hop header stripping, rate
limiting, bounded concurrency, per-request timeouts, structured error
propagation, and graceful signal-driven shutdown all function correctly and are
covered by tests that pass under the race detector.

The two remaining architectural stubs are **MaxTransport** (Send/Receive return
`ErrNotImplemented`) and the **ACK/WINDOW/retry** flow-control layer. Neither
blocks the memory-transport use case. The MAX transport stub is a P1 blocker
only if the system is intended for production messaging-channel deployment.

Test coverage has grown from ~30 tests across 4 packages to **~82 tests across
8 packages**, all passing with `-race`.

---

## 2. Changes Since Original Audit (PR2–PR6)

| PR | Summary | Original status → Now |
|----|---------|----------------------|
| PR 2 | `Config.Validate()`, 28 config tests, startup exits on invalid config | MISSING → DONE |
| PR 2 | Memory transport wired in `buildTransport` (dev/selftest) | PARTIAL → DONE |
| PR 3 | `SafeDialer` with post-resolve IP validation (DNS rebinding) | UNSAFE → DONE |
| PR 3 | CGNAT (`100.64.0.0/10`) and IPv6 link-local (`fe80::/10`) blocked | PARTIAL → DONE |
| PR 3 | All RFC 7230 §6.1 hop-by-hop headers stripped at exit | PARTIAL → DONE |
| PR 4 | Rate limiter wired into `relay.Session.sendFrame` | MISSING → DONE |
| PR 4 | Rate limiter wired into `exit.HTTPExecutor.sendFrame` | MISSING → DONE |
| PR 4 | Taskfile `fmt`, `lint`, `test-race`, `test-unit`, `test-integration`, `all` added | MISSING → DONE |
| PR 5 | Bounded concurrency (`maxPending` in `relay.Session`, HTTP 429) | MISSING → DONE |
| PR 5 | Per-request relay timeout (configurable, default 30 s, HTTP 502 on expiry) | MISSING → DONE |
| PR 6 | `FrameERROR` typed error propagation (`relay.RelayError`, 6 error codes) | STUB → DONE |
| PR 6 | Proxy maps `RelayError.HTTPStatus` → HTTP 403/502/504 | STUB → DONE |
| PR 6 | CLI graceful shutdown: `SIGINT`/`SIGTERM` → `http.Server.Shutdown` (10 s deadline) | PARTIAL → DONE |
| PR 6 | Exit: `context.Canceled` treated as clean stop | PARTIAL → DONE |

---

## 3. Command Results

| Command | Exit code | Notes |
|---------|-----------|-------|
| `task --list` | 0 | 12 tasks: `all`, `build`, `clean`, `fmt`, `lint`, `selftest`, `test`, `test-integration`, `test-race`, `test-unit`, `tidy`, `vet` |
| `task fmt` | 0 | `go fmt ./...` — no changes (all files gofmt-clean after two files needed header fix) |
| `task lint` | 0 | `go vet ./...` — zero issues |
| `task test` | 0 | 8 packages pass; 4 packages have no test files |
| `task test-race` | 0 | All 8 packages pass under `-race`; zero data races detected |
| `task build` | 0 | Produces `bin/chunkbridge` |
| `selftest` | 0 | `PASS: GET /hello` · `PASS: POST /echo` · `PASS: GET /big (128 KB)` |

**Pre-run fix required (recurring pattern):** Two test files
(`internal/exit/http_executor_error_test.go`,
`internal/relay/relay_error_test.go`) had a duplicate `package` declaration on
line 1 that `go fmt` rejects. The extra line was removed before the audit
commands ran. This is a persistent tooling artifact; see §9.

---

## 4. Implementation Matrix

> Status codes: **DONE** · **PARTIAL** · **STUB** · **MISSING** · **DEAD CODE** · **UNSAFE**

### CLI

| Feature | Status | Notes |
|---------|--------|-------|
| `client` command | DONE | config → validate → key-derive → transport → proxy → serve |
| `exit` command | DONE | config → validate → key-derive → transport → executor → run |
| `selftest` command | DONE | 3 checks pass end-to-end with in-memory transport |
| `version` command | DONE | Prints `chunkbridge 0.1.0` |
| Graceful shutdown (SIGINT/SIGTERM) | DONE | `signal.NotifyContext`; client uses 10 s `Shutdown` deadline; exit cancels context |
| Config positional arg | DONE | Defaults to `chunkbridge.{client,exit}.yaml`; overridable as 2nd positional arg |

### Config

| Feature | Status | Notes |
|---------|--------|-------|
| YAML load | DONE | `config.LoadFile` |
| `Config.Validate()` | DONE | 28 tests; validates mode, transport, crypto, listen, proxy, policy, message limits, rate limits, window, ack |
| Startup exit on bad config | DONE | `main.go` calls `cfg.Validate()` before key derivation |
| Safe defaults | DONE | Client: `127.0.0.1:8080`, `BlockPrivateRanges: false`. Exit: `BlockPrivateRanges: true`, blocked ports list |
| Default salt `saltchangeme1234` | PARTIAL | Salt is valid (16 bytes); code comment warns operators; no runtime warning if unchanged |

### Protocol

| Feature | Status | Notes |
|---------|--------|-------|
| CB/1 wire format | DONE | `CB1|D|<sid>|<seq>|<b64(nonce‖ciphertext)>` |
| Frame types defined (9) | DONE | HELLO=1, OPEN=2, DATA=3, ACK=4, WINDOW=5, CLOSE=6, PING=7, PONG=8, ERROR=9 |
| DATA frames wired | DONE | Only frame type used in relay path |
| ERROR frames wired | DONE | `FrameERROR` sent by exit on all failure paths; decoded by `relay.Session.SendRequest` into `*RelayError` |
| ACK frames | STUB | `NewACKFrame`/`IsACK` defined; never sent or received in relay path |
| WINDOW frames | STUB | Frame type defined only |
| CLOSE/PING/PONG frames | STUB | Frame types defined only |
| Sequence numbers | PARTIAL | Assigned atomically per send; AAD binds seq to ciphertext; no seen-seq set for replay/duplicate detection |
| Request ID correlation | DONE | 8-byte random hex; end-to-end match between proxy and exit |
| Chunking (1600-byte payload) | DONE | `Chunk()` tested; `SeqNum` assigned atomically post-chunk |
| Reassembly with eviction | DONE | `Reassembler`: duplicate detection, out-of-order, 60 s timeout eviction |
| `Frame.StreamID` field | DEAD CODE | Defined; never set or read |
| `Envelope` struct | DEAD CODE | Defined in `protocol/envelope.go`; no callers anywhere |
| Final encoded-size check | PARTIAL | `MaxPayloadBytes=1600` limits payload before encryption; encoded b64 length not verified before `Transport.Send` |

### Crypto

| Feature | Status | Notes |
|---------|--------|-------|
| AEAD (XChaCha20-Poly1305) | DONE | 256-bit key, 192-bit nonce |
| Key derivation (Argon2id) | DONE | Configurable time/mem/threads; salt validated to exactly 16 bytes |
| Per-message random nonce | DONE | 24 bytes from `crypto/rand` |
| AAD (session ID + seq num) | DONE | Binds ciphertext to session and sequence position |
| Compression before encryption | DONE | gzip → XChaCha20-Poly1305 |
| No key/passphrase logging | DONE | Confirmed by audit of all log call sites |
| Tamper/wrong-key detection | DONE | Covered by `TestDecryptTampered`, `TestDecryptWrongKey` |
| Per-deployment static salt | PARTIAL | Salt shared across all sessions in a deployment; no per-session salt; documented limitation in `security.md` |

### Transport

| Feature | Status | Notes |
|---------|--------|-------|
| `MemoryTransport` | DONE | Bidirectional; configurable latency and drop rate; 256-message buffers |
| `MaxTransport` – compilation | DONE | Compiles; reads token from env |
| `MaxTransport` – Send | STUB | Returns `ErrNotImplemented` |
| `MaxTransport` – Receive | STUB | Returns `ErrNotImplemented` |
| MAX 429 backoff | STUB | `AdaptiveRateLimiter.On429()` and `BackoffDuration()` implemented; never called from transport layer |
| MAX polling / webhook | STUB | Not implemented |

### HTTP Proxy (client side)

| Feature | Status | Notes |
|---------|--------|-------|
| Local proxy server | DONE | `HTTPProxy.Serve` on configured `127.0.0.1` address |
| CONNECT rejection | DONE | Returns 501 |
| Request body limit | DONE | `io.LimitReader` at 10 MB |
| Bounded concurrency | DONE | `maxPending` in `relay.Session`; 429 on overflow |
| Per-request relay timeout | DONE | Configurable via `ProxyConfig.RequestTimeoutMs`; 502 on expiry |
| Graceful `Shutdown` | DONE | `http.Server` created in `NewHTTPProxy`; `Shutdown(ctx)` is race-safe |
| `RelayError` → HTTP status | DONE | `errors.As` maps `RelayError.HTTPStatus` directly; 403/502/504 tested |

### Exit Executor

| Feature | Status | Notes |
|---------|--------|-------|
| Request decode + reassemble | DONE | `DecodeMessage` → `Reassembler.Add` → `policy.CheckRequest` → HTTP |
| Policy enforcement | DONE | Scheme, port, domain, private-IP, SafeDialer |
| DNS rebinding mitigation | DONE | `SafeDialer` re-validates resolved IP after DNS; blocks loopback, RFC1918, CGNAT, metadata, link-local |
| Hop-by-hop header stripping | DONE | Strips `Connection`, `Proxy-Connection`, `Proxy-Authorization`, `Transfer-Encoding`, `TE`, `Trailers`, `Upgrade` and any headers named in `Connection:` value |
| Outbound HTTP call | DONE | `http.Client` with `RequestTimeoutSec` deadline |
| Upstream timeout detection | DONE | `errors.As(err, &netErr) && netErr.Timeout()` → `ErrCodeUpstreamTimeout` / 504 |
| Response size limit | DONE | `io.LimitReader` + length check; `ErrCodeResponseTooLarge` → 502 |
| `sendError` uses `FrameERROR` | DONE | All 6 error codes send typed `FrameERROR` payload |
| Content-type policy | PARTIAL | `AllowedContentTypes` enforced when non-empty; default is empty = no restriction |

### Policy

| Feature | Status | Notes |
|---------|--------|-------|
| Domain allow list | DONE | Wildcard (`*.example.com`) supported; empty = all domains allowed |
| Port blocklist | DONE | Default blocks 22, 25, 465, 587, 6379, 5432, 3306, 27017 |
| Private IP block | DONE | RFC1918, loopback, link-local, CGNAT (`100.64.0.0/10`), IPv6 link-local (`fe80::/10`), metadata (`169.254.0.0/16`), unspecified, IPv4-mapped IPv6 |
| DNS rebinding | DONE | SafeDialer re-checks post-resolve IP |
| Allowed schemes | DONE | Default: `http`, `https` only |

### Rate Limiting

| Feature | Status | Notes |
|---------|--------|-------|
| Token bucket | DONE | Implemented and tested; thread-safe with `sync.Mutex` |
| Adaptive limiter (3 buckets) | DONE | Global, data, control buckets |
| Rate limiter in relay send path | DONE | `WaitForToken` called per chunk in `session.sendFrame` |
| Rate limiter in exit send path | DONE | `WaitForToken` called per chunk in `executor.sendFrame` |
| `On429()` feedback | PARTIAL | `AdaptiveRateLimiter.On429()` halves data RPS; never called from transport (MaxTransport is a stub) |
| Sliding window | MISSING | `WindowConfig` parsed; no window logic anywhere |
| ACK-based retry | MISSING | `AckConfig` parsed; no ACK send/recv logic in relay path |

### Observability

| Feature | Status | Notes |
|---------|--------|-------|
| Structured logging (`slog`) | DONE | JSON or text handler; `slog`-based with configurable level |
| No secret logging | DONE | Keys, tokens, and bodies are not logged |
| Atomic metrics counters | DONE | 21 counters across transport, protocol, proxy, exit, rate-limit categories |
| Metrics exposure | MISSING | Counters live in-process only; no HTTP endpoint or `SIGUSR1` dump |

### Documentation

| Document | Status | Notes |
|----------|--------|-------|
| `README.md` | DONE | Accurate; selftest instructions correct |
| `docs/protocol.md` | DONE | Wire format accurate; notes that most frame types are not wired |
| `docs/architecture.md` | DONE | Accurate post-PR6; includes FrameERROR table and graceful shutdown section |
| `docs/security.md` | DONE | Honest about replay protection gap; DNS rebinding section present post-PR3 |
| `docs/rate-limits.md` | DONE | Accurately notes ACK/WINDOW as parsed-only and `On429()` as not wired |
| `docs/max-transport.md` | DONE | Clearly states stub status |

---

## 5. Test Quality Assessment

### Coverage by package

| Package | Tests | Timing-sensitive | Notes |
|---------|-------|-----------------|-------|
| `internal/config` | 28 | No | Table-driven; all deterministic |
| `internal/policy` | 17 | No | Includes 6 SafeDialer / DNS-rebinding tests using real DNS resolution on loopback/public IPs |
| `internal/relay` | 9 | Marginal | `TestSessionPendingCleanedAfterTimeout` waits up to 200 ms; `TestRelayErrorFrameForMissingRequestIgnored` waits 100 ms — both use channel selects not `time.Sleep`, robust under load |
| `internal/proxy` | 6 | One | `TestHTTPProxyMapsUpstreamTimeoutTo504` runs a real 1 s HTTP timeout — genuine timeout assertion, not a sleep; acceptable |
| `internal/exit` | 10 | Marginal | `TestHTTPExecutorSendFrameUsesRateLimiter` polls rate limiter at 5 ms intervals; passes reliably under race detector |
| `internal/ratelimit` | 5 | One | `TestRPSRefill` sleeps 200 ms to check token accumulation; deterministic at that timescale |
| `internal/crypto` | (cached) | No | AEAD tamper / wrong-key tests |
| `internal/protocol` | (cached) | No | Chunk, encode, decode round-trips |
| `tests/integration` | 3 | No | GET, POST, 1 MB over memory transport — all deterministic |

**Packages with no tests:** `cmd/chunkbridge`, `internal/compress`, `internal/observability`, `internal/transport`

### Recurring tooling issue
The code-generation tool inserts a spurious `package X` declaration before
`package X_test` on line 1 of newly created test files. This broke `go fmt`
at audit start. The pattern has recurred in every PR that added new test files.
The fix is always: remove the extra line. A pre-commit or CI `go build ./...`
step would catch this before it reaches main.

---

## 6. Verified End-to-End Flows

All three flows tested by selftest and integration tests:

| Flow | Path | Verified |
|------|------|---------|
| GET request | proxy → encrypt → chunk → memory transport → reassemble → decrypt → exit policy → HTTP GET → encrypt → chunk → memory transport → reassemble → decrypt → proxy → HTTP 200 | ✓ selftest + integration |
| POST with body | Same as above; 10-byte body echoed | ✓ selftest + integration |
| 128 KB response | Multi-chunk reassembly (Chunk/Reassembler path fully exercised) | ✓ selftest |
| 1 MB response | Multi-chunk reassembly | ✓ integration |
| Policy denial | Exit sends FrameERROR(policy_denied); proxy returns 403 | ✓ unit tests |
| Upstream timeout | Exit sends FrameERROR(upstream_timeout); proxy returns 504 | ✓ unit tests |
| Upstream unavailable | Exit sends FrameERROR(upstream_unavailable); proxy returns 502 | ✓ unit tests |
| Relay timeout | `SendRequest` returns timeout error; proxy returns 502 | ✓ unit tests |
| Concurrency limit | Session rejects when `maxPending` reached; proxy returns 429 | ✓ unit tests |
| Graceful shutdown | SIGINT → `Shutdown(10s)` → in-flight requests drain | ✓ unit test |

---

## 7. Remaining Gaps

### P1 — Blocks real deployment

| Gap | Detail |
|-----|--------|
| `MaxTransport` stub | `Send`/`Receive` return `ErrNotImplemented`. The system cannot be used over any messaging channel without this implementation. |
| Default salt unchanged at runtime | `saltchangeme1234` is the default; if an operator forgets to rotate it, two deployments sharing the same passphrase will share the same key. A startup log warning when salt matches the default value would reduce this risk. |

### P2 — Functional gaps that reduce reliability or security posture

| Gap | Detail |
|-----|--------|
| ACK/retry/sliding-window | `AckConfig`, `WindowConfig`, `NewACKFrame`, `IsACK` all exist but nothing in the relay path sends or processes ACKs. Dropped chunks are not retried; the request just times out. |
| `On429()` feedback loop | `AdaptiveRateLimiter.On429()` halves the data RPS but is never called. Will matter when `MaxTransport` is implemented. |
| Final encoded-size check | `MaxPayloadBytes=1600` caps the pre-encryption payload; the resulting b64 string length is not verified against `MessageConfig.MaxB64Chars` before `Transport.Send`. Currently harmless; must be checked when `MaxTransport` is active. |
| Content-type safe defaults | `AllowedContentTypes` is empty by default, allowing all content types through. A safe-default deny list for binary media types would reduce bandwidth abuse. |
| CI workflow | No `.github/workflows/` directory. Merges to main are not automatically validated. The duplicate-package-declaration regression would be caught immediately by CI. |

### P3 — Quality and hygiene

| Gap | Detail |
|-----|--------|
| `Frame.StreamID` | Defined, never set or read. Remove or implement. |
| `Envelope` struct (`protocol/envelope.go`) | Defined, no callers. Remove. |
| Metrics exposure | 21 counters collected; no HTTP endpoint or signal handler to inspect them. |
| `cmd/chunkbridge` has no unit tests | Entry-point logic (`buildTransport`, `buildRateLimiter`, `deriveKey`) is untested. |
| `internal/compress`, `internal/transport` have no unit tests | Low priority; compress is a thin wrapper, transport/memory is exercised by integration tests. |

---

## 8. MVP Readiness Assessment

| Criterion | Memory transport | MAX transport |
|-----------|-----------------|---------------|
| End-to-end encryption active | ✓ | ✓ (would be, once wired) |
| Policy enforcement (DNS rebinding, private IPs, ports, schemes) | ✓ | ✓ |
| Rate limiting wired | ✓ | ✓ (bucket ready; On429 not wired) |
| Bounded concurrency | ✓ | ✓ |
| Per-request timeouts | ✓ | ✓ |
| Structured error propagation | ✓ | ✓ |
| Graceful shutdown | ✓ | ✓ |
| Config validation at startup | ✓ | ✓ |
| Transport functional | ✓ | ✗ (stub) |
| Tests passing under race detector | ✓ | N/A |
| CI pipeline | ✗ | ✗ |

**Memory transport verdict:** `MVP_READY`. All mandatory data-path, security, and reliability features are implemented and tested.

**MAX transport verdict:** `PARTIAL_MVP`. Everything except the transport layer itself is ready. Once `MaxTransport.Send` and `Receive` are implemented, the only remaining work to reach MVP is the `On429()` feedback loop and the encoded-size guard.

---

## 9. Recommended Next PRs

### PR 7 — Housekeeping (low risk, high value)
- Remove `protocol/envelope.go` (dead code)
- Remove or document `Frame.StreamID`
- Add `CHUNKBRIDGE_SALT_DEFAULT_WARNING` log at startup when salt matches `saltchangeme1234`
- Add `.github/workflows/ci.yml`: `go fmt -l` (fail on output) + `go vet ./...` + `go test -race ./...`

### PR 8 — `MaxTransport` implementation
- Implement `Send` and `Receive` using the Max.ai messaging API
- Wire `On429()` and `BackoffDuration()` into the transport send loop
- Add encoded-size check before each `Transport.Send`
- Add `MaxTransport` integration test (mock HTTP server)

### PR 9 — ACK and retry (after PR 8)
- Implement ACK sender in `relay.Session` (every N chunks or after timeout)
- Implement ACK receiver in `exit.HTTPExecutor`
- Implement retry on timeout using `AckConfig.MaxRetries`
- This completes the reliability layer and makes the P2 gaps above disappear

### PR 10 — Observability (after PR 8)
- Add `/metrics` HTTP endpoint on a separate localhost port exposing all `Metrics` counters
- Add `SIGUSR1` handler as an alternative dump path
- Add `cmd/chunkbridge` unit tests for `buildTransport`, `buildRateLimiter`

---

## 11. Post-Audit Update — PR 7 (MaxTransport implementation)

**Date:** 2026-05-04

`MaxTransport` is **no longer a stub**. `Send` and `Receive` are fully
implemented against assumed MAX Bot API endpoints and tested with
`httptest.Server`. All tests pass under the race detector.

| Item | Before PR 7 | After PR 7 |
|------|------------|------------|
| `MaxTransport.Send` | STUB (`ErrNotImplemented`) | DONE (mocked) |
| `MaxTransport.Receive` | STUB (`ErrNotImplemented`) | DONE (mocked, long-poll) |
| `On429()` feedback | STUB (never called) | DONE — `WithOn429` callback; called on 429 in both send and receive paths |
| `Retry-After` parsing | MISSING | DONE — returned in `*RateLimitError` |
| Config: `base_url`, `peer_chat_id` | MISSING | DONE — required fields, validated at startup |
| Config: `poll_timeout_sec` | MISSING | DONE |
| Transport tests | MISSING | 16 tests in `maxapi_test.go` + 5 config tests |

**Remaining live validation gap:** The assumed API endpoint paths, JSON field
names, and authentication scheme have not been validated against the real Max.ai
production API. See `docs/max-transport.md §Remaining Gaps` for the full list.

**Overall classification remains `MVP_READY` (memory transport)** and is now
upgraded to **`PARTIAL_MVP` for MAX transport** — the implementation is
complete but untested against the live API.


**Classify as `MVP_READY`** with the caveat that "MVP" is scoped to the
memory-transport configuration. The core relay, security, and reliability
features that an MVP requires are all present, correct, and well-tested.

The path to production deployment (MAX transport) requires one more focused PR
(PR 8 above) and is not blocked by design or architectural issues — it is
straightforward implementation work on a well-defined stub.

Do not remove the `ACK`/`WINDOW` config fields or dead code before PR 7 makes a
conscious decision about them; the fields are parsed and validated, and removing
them would be a breaking config-file change.
