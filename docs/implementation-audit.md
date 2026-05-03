# chunkbridge Implementation Audit

**Date:** 2026-05-03  
**Auditor:** automated code + runtime inspection  
**Branch:** main  

---

## 1. Executive Summary

**Current status: `PARTIAL_MVP`**

The core data path compiles, tests pass (including race detector), and the selftest verifies end-to-end relay over an in-memory transport with encryption active. GET, POST, and 128 KB responses work correctly in both the selftest and integration tests.

However, several MVP-required subsystems exist as isolated, untethered code: the rate limiter, ACK frames, WINDOW frames, CLOSE/ERROR frames, and the sliding window are all fully defined but **never called from the relay path**. Configuration validation is absent — the binary silently ignores a missing salt and crashes at key derivation instead of failing at startup with a clear message. DNS rebinding is not mitigated at the exit node. The MAX transport returns `ErrNotImplemented` for every call and cannot be used. Metrics are collected into atomic counters but never exposed. The Taskfile is missing the `test-race`, `fmt`, and `lint` tasks (though all source files are already `gofmt`-clean).

The project is honest about what is and is not implemented in its documentation. It is not misleadingly overclaiming, but the rate limiting and flow control sections of the docs and config structs describe behaviour that does not exist yet in the relay path.

---

## 2. Command Results

| Command | Result | Notes |
|---|---|---|
| `task --list` | PASS (exit 0) | Lists 9 tasks. No `fmt`, `lint`, or `test-race` tasks exist. |
| `task tidy` | PASS (exit 0) | `go mod tidy` succeeds. |
| `task fmt` | **MISSING** | Task does not exist. Fallback: `gofmt -l .` produces **no output** — all source files are correctly formatted. |
| `task lint` | **MISSING** | Task does not exist. `task vet` runs `go vet ./...` (exit 0, no issues). |
| `task test` | PASS (exit 0) | 4 packages have tests; 9 packages have no test files. |
| `task test-race` | **MISSING** | Task does not exist. Fallback: `go test -race ./... -timeout 120s` PASS (exit 0). |
| `task build` | PASS (exit 0) | Produces `bin/chunkbridge`. |
| `task selftest` | PASS (exit 0) | All 3 checks pass: GET /hello, POST /echo, GET /big (128 KB). |

---

## 3. Implementation Matrix

| Area | Expected | Current | Status | Evidence | Action |
|---|---|---|---|---|---|
| **CLI – `client` command** | `chunkbridge client [config]` | Present, positional arg for config | DONE | `main.go:56` | — |
| **CLI – `exit` command** | `chunkbridge exit [config]` | Present | DONE | `main.go:98` | — |
| **CLI – `selftest` command** | In-process end-to-end test | Present, 3 checks pass | DONE | `main.go:127` | — |
| **CLI – `version` command** | Prints version | Present, prints `chunkbridge 0.1.0` | DONE | `main.go:32` | — |
| **CLI – context/cancellation** | Graceful shutdown on signal | `runClient` has no signal handler; proxy server is never `Shutdown()`-ed | PARTIAL | `main.go:57–96` | Add `signal.NotifyContext` + `http.Server.Shutdown` |
| **CLI – `--config` flag** | `--config <path>` flag | Positional arg only; README documents positional | PARTIAL | `main.go:58` | Low priority; document clearly |
| **Config – YAML loading** | Read and parse YAML | `config.LoadFile` implemented | DONE | `config/load.go` | — |
| **Config – validation** | Required fields checked at startup | No validation. Missing `salt`, zero Argon2 params, or empty `passphrase_env` are accepted silently and fail later at key derivation with a cryptic error | MISSING | `config/load.go:10–21` | Add `Config.Validate()` called before `deriveKey` |
| **Config – safe defaults** | `127.0.0.1` binding | `DefaultClientConfig` binds to `127.0.0.1:8080` | DONE | `config/defaults.go:8` | — |
| **Config – exit defaults** | Private ranges blocked by default | `DefaultExitConfig` sets `BlockPrivateRanges = true` | DONE | `config/defaults.go:54` | — |
| **Config – example configs** | Usable example files | Present at `.example.chunkbridge.client.yaml` and `.example.chunkbridge.exit.yaml`. Both use `type: max` transport (the only usable transport for production) | DONE | root dir | — |
| **Config – insecure example salt** | Operators warned about default salt | Example configs use `salt: "saltchangeme1234"`. No startup warning if default/example value is detected | UNSAFE | `.example.*.yaml` | Add a startup warning or entropy check |
| **Config – memory transport via CLI** | Dev/test mode without MAX | `buildTransport` has no `case "memory"` — only `case "max"` is handled; all other types return an error | PARTIAL | `main.go:300–307` | Add `case "memory"` in `buildTransport` for development use |
| **Protocol – CB/1 versioning** | `CB1` prefix enforced | Enforced on decode; `ErrUnknownVersion` returned for non-`CB1` prefix | DONE | `protocol/decode.go:33` | — |
| **Protocol – frame types defined** | All 9 frame types | All 9 defined: HELLO, OPEN, DATA, ACK, WINDOW, CLOSE, PING, PONG, ERROR | DONE | `protocol/frame.go` | — |
| **Protocol – frame types used** | All or stated subset used in relay | Only `FrameDATA` is ever constructed or handled. HELLO, OPEN, ACK, WINDOW, CLOSE, PING, PONG, ERROR are defined but never sent or received anywhere outside their own definition files | STUB | grep confirms zero callsites | Remove unused types or implement them |
| **Protocol – sequence numbers** | Monotonic, verified | Assigned on send via `atomic.Uint32`. Never verified or tracked on receive (no replay/duplicate detection at the protocol layer) | PARTIAL | `relay/session.go:109`, `exit/http_executor.go:185` | Add seen-seq tracking |
| **Protocol – stream IDs** | Multi-stream support | `Frame.StreamID` defined but never set or read anywhere | STUB | `protocol/frame.go:23` | Remove field or implement |
| **Protocol – request IDs** | Request/response correlation | Used end-to-end for response dispatch | DONE | `relay/session.go:75–82` | — |
| **Protocol – ACK handling** | ACK frames sent and processed | `NewACKFrame` and `IsACK` defined. Never called from relay, session, proxy, or exit | STUB | grep confirms zero callsites in relay path | Wire ACK into session layer |
| **Protocol – WINDOW frames** | Sliding window updates | Frame type defined. No implementation exists anywhere | STUB | `protocol/frame.go:10` | Implement or defer |
| **Protocol – CLOSE/ERROR frames** | Clean stream teardown | Frame types defined. No implementation exists anywhere | STUB | `protocol/frame.go:11,14` | Implement error propagation |
| **Protocol – Envelope struct** | Used for wire framing | `protocol/envelope.go` defines `Envelope` struct; it is completely unused — the wire format is a plain text string, not a JSON Envelope | DEAD CODE | grep confirms zero callsites | Remove `envelope.go` |
| **Protocol – chunking** | Split payloads at 1600 bytes | `Chunk()` implemented and tested | DONE | `protocol/chunk.go` | — |
| **Protocol – reassembly** | Collect chunks, timeout eviction | `Reassembler` with duplicate detection, out-of-order handling, and timeout eviction | DONE | `protocol/reassembly.go` | — |
| **Protocol – malformed frame rejection** | Error on bad input | `DecodeMessage` rejects short, wrong-version, non-base64, tampered, wrong-key messages | DONE | `protocol/decode.go`, encode_test.go | — |
| **Protocol – encoded size enforcement** | Verify ≤ 3400 b64 chars before send | `MaxPayloadBytes = 1600` limits payload before encryption, but the final encoded string length is never checked before calling `t.Send` | PARTIAL | `protocol/chunk.go:9` | Verify encoded length post-encode |
| **Frame encode/decode** | Full round-trip with compression + encryption | Tested by unit tests and integration tests | DONE | `protocol/encode_test.go`, integration tests | — |
| **Crypto – AEAD cipher** | Strong AEAD | XChaCha20-Poly1305 (256-bit key, 192-bit nonce) | DONE | `crypto/aead.go` | — |
| **Crypto – KDF** | Argon2id | Argon2id with configurable time/memory/threads | DONE | `crypto/keyderive.go` | — |
| **Crypto – per-session salt** | Unique salt per deployment/session | Salt is a static config value, shared for all sessions in a deployment. No per-session salt generation | PARTIAL | `config/config.go:42` | Document this limitation; auto-generate salt at first start |
| **Crypto – per-message nonce** | Random nonce per message | 24 random bytes generated per encrypt call | DONE | `crypto/aead.go:21` | — |
| **Crypto – AAD** | Bind ciphertext to session + seq | `sessionID + "|" + seqNum` used as AAD | DONE | `protocol/encode.go:28` | — |
| **Crypto – compression before encryption** | gzip then encrypt | gzip before XChaCha20-Poly1305 | DONE | `protocol/encode.go:22` | — |
| **Crypto – no secret logging** | Keys/passphrases never logged | No logging of key material observed | DONE | Audit of logger + relay path | — |
| **Crypto – tamper detection** | Wrong key / tampered ciphertext fails | Tested by `TestDecryptTampered`, `TestDecryptWrongKey` | DONE | `crypto/aead_test.go` | — |
| **Crypto – encryption in relay path** | Encryption active end-to-end, not only in unit tests | `EncodeMessage`/`DecodeMessage` called in `relay/session.go` and `exit/http_executor.go` | DONE | Verified by integration tests | — |
| **Memory transport** | Real bidirectional in-process transport | Bidirectional with `LatencyMs` and `DropRate` options; buffered channels (256) | DONE | `transport/memory.go` | — |
| **MAX transport** | Functional MAX Bot API transport | Compiles, returns `ErrNotImplemented` for every `Send` and `Receive` call. Reads token from env at startup. HTTP client created but never used | STUB | `transport/maxapi.go:48,54` | Implement once API endpoints are available |
| **MAX – Authorization header** | Bearer token sent | `token` field stored, never used (Send/Receive are stubs) | STUB | `transport/maxapi.go` | — |
| **MAX – message size limits** | Enforce 3400 b64 char limit | Config struct has `MaxB64Chars` field; no code enforces it at send time | PARTIAL | `config/config.go:57` | Check encoded length before each send |
| **MAX – 429 handling** | Backoff and retry on 429 | `AdaptiveRateLimiter.On429()` and `BackoffDuration()` exist. Never called from transport or relay path | STUB | grep confirms zero callsites in transport path | Wire into MAX transport when implemented |
| **MAX – polling/webhook** | Message receive mechanism | Not implemented (stub) | STUB | `transport/maxapi.go:54` | Implement long-poll or webhook |
| **HTTP proxy** | Local HTTP proxy on `127.0.0.1` | Implemented; CONNECT rejected with 501; body limited to 10 MB | DONE | `proxy/http_proxy.go` | — |
| **HTTP proxy – sensitive header handling** | Strip hop-by-hop and dangerous headers | Only `Proxy-Connection` and `Proxy-Authorization` are stripped outbound. `Host` override, `Transfer-Encoding`, `Connection`, `TE`, `Trailers`, `Upgrade` not stripped | PARTIAL | `exit/http_executor.go:143` | Strip all hop-by-hop headers per RFC 7230 §6.1 |
| **Exit executor** | Receive request, make outbound call, return response | Fully wired: receive → decode → reassemble → policy check → HTTP call → encode → send | DONE | `exit/http_executor.go` | — |
| **Exit executor – content-type policy** | Block disallowed content types | `CheckContentType` called only if `AllowedContentTypes` is non-empty; default empty = all allowed | PARTIAL | `exit/http_executor.go:155` | Document this; consider a safe default blocklist for binary types |
| **Rate limiting – token bucket** | Implemented | `TokenBucket` and `AdaptiveRateLimiter` fully implemented and tested | DONE | `ratelimit/token_bucket.go`, `ratelimit/adaptive.go` | — |
| **Rate limiting – wired in relay** | Used on every send | Rate limiter **never imported or called** in `relay`, `proxy`, `exit`, or `transport` packages | MISSING | grep confirms no callsites | Wire `AdaptiveRateLimiter` into `relay.Session.sendFrame` |
| **Rate limiting – control priority** | Control frames not starved | No priority mechanism exists; all sends go through the same transport channel | MISSING | `relay/session.go` | Implement priority queue or separate send path for control frames |
| **Sliding window / flow control** | AIMD window controlling in-flight chunks | Config structs have `WindowConfig`. No sliding window logic exists anywhere | MISSING | `config/config.go:62–68` | Implement or remove config fields and docs |
| **ACK every N frames** | Receiver sends ACK after N data frames | Config has `AckConfig.IntervalMs`. No ACK sending logic exists in relay path | MISSING | `ratelimit/adaptive.go`, `config/config.go` | Implement or remove |
| **Retry on timeout** | Resend if no ACK within timeout | Config has `AckConfig.TimeoutMs` and `MaxRetries`. No retry logic exists | MISSING | `config/config.go:70–75` | Implement or remove |
| **Backoff with jitter** | Exponential backoff on 429 | `BackoffDuration()` exists. Never called from relay path | STUB | `ratelimit/adaptive.go:56` | Wire into MAX transport |
| **Policy – domain allow list** | Enforced at exit | `IsAllowedDomain` called on every request; also checked client-side | DONE | `policy/policy.go:60` | — |
| **Policy – private network block** | Block RFC1918, loopback, link-local | Blocks `10/8`, `172.16/12`, `192.168/16`, `127/8`, `169.254/16`, `::1/128`, `fc00::/7` | PARTIAL | `policy/policy.go:84` | Add `fe80::/10` (IPv6 link-local), `100.64.0.0/10` (CGNAT) |
| **Policy – DNS rebinding** | Post-resolve IP check | Policy only checks hostname at URL-parse time. DNS resolution happens in `http.Client.Do`. A hostname resolving to 192.168.x.x bypasses all private IP checks | UNSAFE | `policy/policy.go:68` | Use a custom `http.Transport` with a dial hook to re-validate resolved IPs |
| **Policy – cloud metadata endpoint** | Block 169.254.169.254 | Covered by `169.254.0.0/16` — but only for literal IPs in the URL, not for DNS-resolved hostnames | PARTIAL | `policy/policy.go:84` | Same fix as DNS rebinding above |
| **Policy – port blocklist** | Blocked ports enforced | `IsAllowedPort` called in `CheckRequest` | DONE | `policy/ports.go` | — |
| **Policy – unsupported schemes** | Reject non-HTTP(S) | `AllowedSchemes` checked; default includes `http` and `https` only | DONE | `policy/policy.go:37` | — |
| **Policy – max response size** | Enforced before buffering | `io.LimitReader` + length check in exit executor | DONE | `exit/http_executor.go:160` | — |
| **Policy – max request size** | Enforced before forwarding | `io.LimitReader` at 10 MB in proxy | DONE | `proxy/http_proxy.go:83` | — |
| **Policy – extension/content-type blocks** | Block video/audio/font etc. | Only enforced if `AllowedContentTypes` list is non-empty in config. Default is empty = no block | PARTIAL | `exit/http_executor.go:153` | Provide a safe default deny list in `DefaultExitConfig` |
| **Observability – structured logging** | `slog`-based structured logs | `Logger` wraps `slog` with JSON or text handler | DONE | `observability/logger.go` | — |
| **Observability – request IDs in logs** | Log request IDs, stream IDs, seq nums | Logger is wired into proxy and exit but logs are sparse; request IDs, seq nums, and chunk counts are not logged in normal flow | PARTIAL | `proxy/http_proxy.go`, `exit/http_executor.go` | Add structured log fields per request |
| **Observability – secret redaction** | No bodies, tokens, or keys logged | No body logging observed; no token/key logging observed | DONE | Audit of all log call sites | — |
| **Observability – metrics counters** | Atomic counters | 18 counters across transport, protocol, proxy, exit, and rate-limit categories | DONE | `observability/metrics.go` | — |
| **Observability – metrics exposed** | HTTP endpoint or on-signal dump | Counters are only in-process; no HTTP endpoint, no signal handler to dump them | MISSING | `observability/metrics.go` | Add `/metrics` endpoint or `SIGUSR1` handler |
| **Tests – unit** | Protocol, crypto, policy, ratelimit | Present and passing | DONE | See §8 | — |
| **Tests – integration** | End-to-end relay | GET, POST, 1 MB response over memory transport | DONE | `tests/integration/memory_relay_test.go` | — |
| **Tests – race** | No data races | `go test -race ./...` passes | DONE | CI output | — |
| **Tests – coverage gaps** | Compress, config, relay, proxy, exit, transport | 9 out of 13 packages have zero test files | PARTIAL | See §8 | — |
| **Documentation – README** | Accurate, matches code | Mostly accurate; `task fmt` and `task lint` referenced nowhere in README (good), but selftest instructions are correct | DONE | `README.md` | — |
| **Documentation – protocol.md** | Describes wire format | Accurate description of CB1 format, all frame types listed | PARTIAL | `docs/protocol.md` | Note that HELLO/OPEN/ACK/WINDOW/CLOSE/PING/PONG/ERROR are not yet implemented |
| **Documentation – rate-limits.md** | Describes limiter behaviour | Describes ACK window, sliding window, and retry as if configured behaviour, but none of these are wired in the relay path | OVERCLAIMED | `docs/rate-limits.md` | Add a "not yet wired" notice |
| **Documentation – max-transport.md** | Describes MAX transport status | Honestly says "compiles but returns `ErrNotImplemented`" | DONE | `docs/max-transport.md` | — |
| **Documentation – security.md** | Threat model and limitations | Honest about replay protection gap, traffic analysis, forward secrecy. Does not mention DNS rebinding risk | PARTIAL | `docs/security.md` | Add DNS rebinding section |
| **Taskfile completeness** | All workflow tasks present | Missing: `fmt`, `lint`, `test-race` | PARTIAL | `Taskfile.yml` | Add missing tasks |
| **Code formatting** | All files `gofmt`-clean | `gofmt -l .` produces no output — all files are correctly formatted | DONE | Verified by running `gofmt -l .` | — |

---

## 4. What Actually Works End-to-End

All of the following were verified by running code, not by reading comments.

**Local selftest (`task selftest`)**  
PASS. The selftest creates an in-memory transport pair, starts a client proxy and an exit executor in-process, then:
- GET `/hello` → `200 "hello from exit"` ✓  
- POST `/echo` with `{"msg":"test"}` → `200 echoed body` ✓  
- GET `/big` (128 KB response) → `200`, correct body length ✓  
Encryption and chunking/reassembly are exercised in this path.

**GET relay (integration test `TestMemoryRelayGET`)**  
PASS. Real HTTP server ← memory transport → proxy. Status and body verified.

**POST relay (integration test `TestMemoryRelayPOST`)**  
PASS. Body round-trip verified with JSON payload.

**Large response (integration test `TestMemoryRelay1MBResponse`)**  
PASS. 1 048 576 bytes delivered intact. Chunking and reassembly confirmed to work at scale.

**Crypto tamper detection**  
PASS. Wrong key, tampered ciphertext, and changed AAD all produce errors, verified by unit tests.

**Policy enforcement**  
PASS. Domain allow list, private IP block, port block, response size limit, wildcard domain — all verified by unit tests.

**Rate limiter (isolated)**  
PASS. Token bucket burst, refill, adaptive On429, backoff floor — all verified by unit tests. **Not verified in relay path** (never called there).

**MAX transport status**  
NOT WORKING. Every `Send` and `Receive` call returns `ErrNotImplemented`. Confirmed by code inspection and by attempting to start `chunkbridge client` with a MAX config (fails at token env check before even reaching Send).

**Error path**  
PARTIAL. Policy violations return 403 to the HTTP client. Outbound failures return 502. CONNECT returns 501. All verified by code inspection; only the policy path is unit-tested.

---

## 5. Critical Gaps

### GAP-01: Rate limiter not wired into relay path
**Severity: P1**  
**Why it matters:** The entire rate-limiting subsystem (token buckets, adaptive backoff, 429 response) is implemented and tested in isolation but never called from any part of the relay path. The binary sends messages at memory speed with no throttling. When the MAX transport is connected, the first real use will immediately overwhelm the platform API.  
**Evidence:** `grep -r "AdaptiveRateLimiter\|ratelimit\." internal/relay/ internal/proxy/ internal/exit/` returns nothing.  
**Recommended fix:** Import `ratelimit` in `relay/session.go`; call `rl.AllowData()` before each chunk send; wire `On429()` into the MAX transport's error handling.  
**Suggested test:** Unit test in `relay` package that verifies sends are blocked when the limiter is exhausted; integration test that injects a simulated 429 and verifies backoff delay.

---

### GAP-02: DNS rebinding allows SSRF at exit node
**Severity: P1**  
**Why it matters:** `policy.CheckRequest` validates the hostname in the URL string before any DNS lookup. The exit node's `http.Client.Do` call resolves the hostname to an IP address that is never re-checked against the private IP blocklist. An attacker who controls DNS can point a permitted hostname (or any domain if the allow-list is empty) to `192.168.0.1`, `169.254.169.254`, or any other internal address, bypassing the entire SSRF defence.  
**Evidence:** `policy/policy.go:68` — the comment explicitly notes "we skip DNS resolution here"; `exit/http_executor.go:130` — `e.client.Do(httpReq)` with no post-resolution check.  
**Recommended fix:** Replace `exit.HTTPExecutor`'s `http.Client` with one using a custom `net.Dialer` that calls `isPrivateIP` on each resolved address before connecting.  
**Suggested test:** Test where policy has `BlockPrivateRanges = true` and request URL hostname resolves to `127.0.0.1`; verify the connection is refused.

---

### GAP-03: Config validation is absent
**Severity: P1**  
**Why it matters:** `LoadFile` unmarshals YAML with no field validation. An empty `crypto.salt`, an empty `crypto.passphrase_env`, or zero Argon2 parameters are silently accepted; the binary crashes at key derivation with a generic error rather than a startup validation failure. Operators cannot distinguish a bad config from a code bug.  
**Evidence:** `config/load.go:10–21` — only `yaml.Unmarshal` errors are checked.  
**Recommended fix:** Add a `Config.Validate() error` method checking: `salt` is non-empty and exactly 16 bytes, `passphrase_env` is non-empty, `argon2_*` params are non-zero, `listen.address` is not empty, transport type is known.  
**Suggested test:** Unit tests for `Validate()` covering each invalid input.

---

### GAP-04: ACK, WINDOW, CLOSE, PING/PONG, ERROR frames are dead code
**Severity: P2**  
**Why it matters:** The protocol spec (frame.go, protocol.md) defines 9 frame types. Only `FrameDATA` is ever constructed or processed. The other 8 are defined constants that create an expectation of functionality that does not exist. The ACK-based retry system (config `ack.timeout_ms`, `max_retries`) is entirely inactive. Lost messages are silently discarded with no recovery.  
**Evidence:** grep confirms zero callsites outside definition files.  
**Recommended fix:** Either implement ACK/CLOSE/ERROR in the session layer, or remove the unused frame types and associated config fields until they are implemented. Do not leave dead code that implies functionality.  
**Suggested test:** Test that a `FrameACK` sent back causes the session to advance its acknowledged-sequence counter.

---

### GAP-05: Sliding window flow control not implemented
**Severity: P2**  
**Why it matters:** `config.WindowConfig` (initial_size, max_size, min_size) and `docs/rate-limits.md` describe a sliding window that controls in-flight chunk count. No such window exists in `relay.Session`. All chunks for all concurrent requests are sent immediately without any in-flight limit. A single large response will send all its chunks without waiting for any ACK.  
**Evidence:** `relay/session.go` — `sendFrame` iterates chunks and sends them all with no window check.  
**Recommended fix:** Implement a simple in-flight counter in `relay.Session`; block sends when window is full; advance window on ACK receipt.  
**Suggested test:** Test that sending N+1 chunks when window size is N blocks until an ACK is received.

---

### GAP-06: Memory transport buffer is unbounded per pending request
**Severity: P2**  
**Why it matters:** `relay.Session.pending` is a `map[string]chan *protocol.Frame`. It grows without bound as concurrent requests are made. A slow or unresponsive exit node leaves channels accumulating. Combined with no backpressure on the request side, a client flood fills the map until the process runs out of memory.  
**Evidence:** `relay/session.go:17` — no capacity check or eviction of `pending`.  
**Recommended fix:** Cap `pending` map size; add a maximum concurrent requests limit; ensure `SendRequest` fails fast if the map is full.  
**Suggested test:** Test that sending more than N concurrent requests returns an error immediately rather than blocking.

---

### GAP-07: Taskfile missing `fmt`, `lint`, and `test-race` tasks
**Severity: P3**  
**Why it matters:** `gofmt -l .` produces no output (all files are correctly formatted), but there is no `task fmt` or `task test-race` target to enforce this in CI or for contributors. A `task lint` task (`go vet`) already exists. Without a `fmt` task, a future contributor may introduce formatting drift that goes unchecked.  
**Evidence:** `task --list` output shows 9 tasks; `fmt`, `test-race` are absent. `gofmt -l .` confirms no current violations.  
**Recommended fix:** Add `fmt: gofmt -w ./...` and `test-race: go test -race ./... -timeout 120s` tasks to `Taskfile.yml`; add a CI step `gofmt -l . | diff /dev/null -` that fails on any output.  
**Suggested test:** CI format check (no Go test needed).

---

### GAP-08: Hop-by-hop headers not stripped at exit node
**Severity: P2**  
**Why it matters:** The exit node forwards all request headers from the proxy to the upstream server, including `Connection`, `Transfer-Encoding`, `TE`, `Trailers`, and `Upgrade`. These are hop-by-hop headers per RFC 7230 §6.1 and must not be forwarded by a proxy. Forwarding them can confuse upstream servers or enable smuggling attacks.  
**Evidence:** `exit/http_executor.go:138–145` — only `Proxy-Connection` and `Proxy-Authorization` are removed.  
**Recommended fix:** Delete all hop-by-hop headers before constructing the outbound `http.Request`.  
**Suggested test:** Test that a request with `Connection: keep-alive` does not forward that header to the upstream.

---

### GAP-09: Metrics are never observable
**Severity: P3**  
**Why it matters:** 18 atomic counters track meaningful operational data (messages sent/received, relay errors, rate-limit hits, etc.) but there is no way to read them — no HTTP endpoint, no signal handler, no log dump. The counters accumulate and are discarded on process exit.  
**Evidence:** `observability/metrics.go` — counters only; no export mechanism.  
**Recommended fix:** Add a `GET /metrics` handler (plaintext or Prometheus-compatible) on the listen address or a separate debug port.  
**Suggested test:** Integration test that fetches `/metrics` and verifies counter values after a relay call.

---

## 6. Safety and Security Review

### Open proxy risk
**Low.** The client proxy binds exclusively to `127.0.0.1` by default. HTTPS CONNECT tunnelling is rejected with 501. There is no unauthenticated network-accessible listener. An operator who changes `listen.address` to `0.0.0.0` would expose the proxy, but there is no protection against that.

### Private network access risk at exit node
**Medium.** Private IP blocking is enabled by default in exit mode and covers the major RFC1918 and loopback ranges. However, `fe80::/10` (IPv6 link-local) and `100.64.0.0/10` (CGNAT / RFC 6598) are not in the blocklist. More critically, the DNS rebinding gap (GAP-02) means all private-range checks can be bypassed via DNS. An operator who leaves `domain_allow_list` empty is especially exposed.

### DNS rebinding risk
**High.** Described in GAP-02. The exit node's HTTP client resolves DNS at connection time without any post-resolve IP validation. This is the most significant security gap in the current implementation.

### Secret logging risk
**Low.** The `Logger` wrapper and all log callsites were inspected. No passphrase, derived key, or API token is logged. Request/response bodies are not logged. The risk is that a future developer adds a log line without realising the body or header may contain secrets — there is no redaction middleware enforcing this structurally.

### Authorization/Cookie header logging risk
**Low.** These headers are not logged. They are forwarded by the proxy to the upstream server (intentional behaviour for a proxy), and forwarded in responses back to the local client. No stripping of `Authorization` or `Set-Cookie` on either direction. This is standard proxy behaviour but should be documented as a deliberate choice.

### Crypto misuse risk
**Low.** XChaCha20-Poly1305 is used correctly: random nonce per message, AAD binds each ciphertext to its session and sequence position, tamper detection is verified by tests. The key derivation uses Argon2id with reasonable defaults. The per-session salt gap (static config salt instead of ephemeral salt) reduces the security margin of Argon2id to offline dictionary attacks if the config file is leaked, but does not break the encryption scheme.

### Misleading documentation risk
**Low to medium.** `docs/rate-limits.md` describes ACK window, sliding window, and retry behaviour as if they are active. An operator reading this doc would believe the system has flow control and retry protection when it does not. This needs a clear "not yet implemented" annotation.

### Unsafe defaults
**Low, one exception.** The main defaults are safe (127.0.0.1 binding, private-range block in exit mode, scheme allow-list). The one exception: `domain_allow_list` defaults to empty, meaning all domains are permitted. The security.md doc advises operators to configure this, but there is no startup warning when it is unconfigured in exit mode.

---

## 7. Rate Limit and Backpressure Review

### Actual limiter behaviour
`TokenBucket` and `AdaptiveRateLimiter` are correctly implemented and unit-tested. They have zero effect on the relay because they are never called from `relay.Session`, `proxy.HTTPProxy`, `exit.HTTPExecutor`, or either transport.

### Control frame starvation
Not applicable today because no control frames are sent. When ACK/CLOSE/PING are implemented, all frame types share the same transport channel with no priority. A burst of DATA frames can delay ACK transmission indefinitely.

### ACK batching
Not implemented. `AckConfig.IntervalMs` exists in the config struct but is read nowhere.

### 429 handling
`AdaptiveRateLimiter.On429()` correctly halves `dataRPS`. `BackoffDuration()` returns `1s + rand(0,500ms)`. Neither is called from the relay path or MAX transport. The Retry-After response header is not parsed or respected.

### Queue depth
The memory transport uses unbounded `chan Message` with buffer 256. There is no back-pressure to the caller when the buffer fills — the `Send` call blocks indefinitely until the context is cancelled. There is no per-connection or global queue depth limit.

### Memory exhaustion risk
A single large response is fully buffered in memory twice: once in `exit.HTTPExecutor.handleRequest` (`io.ReadAll` up to `MaxResponseBytes`), and once in `relay.Session.SendRequest` waiting in the `pending` channel. A 10 MB response (the current limit) creates ~20 MB of allocations per concurrent request. No limit on concurrent in-flight requests means concurrent large responses can exhaust available memory.

### Estimated throughput
Over the in-memory transport, the system sustains at least 1 MB / ~0.3s ≈ 3.3 MB/s (from integration test timing). This is bounded by encryption and gzip overhead, not the transport. With real MAX API transport, throughput will be bound by the 5 RPS global limit × ~3400 b64 chars per message = ~17 000 chars/s ≈ ~12 750 bytes/s raw, compressed and encrypted.

---

## 8. Test Coverage Review

### Existing tests

| File | Package | What it tests |
|---|---|---|
| `internal/crypto/aead_test.go` | crypto | Encrypt/Decrypt roundtrip, wrong key, tampered ciphertext, changed AAD |
| `internal/protocol/encode_test.go` | protocol | Encode/Decode roundtrip, malformed input (4 cases), unknown version, tampered ciphertext |
| `internal/protocol/chunk_test.go` | protocol | Single chunk, multi-chunk split, ordered reassembly, out-of-order reassembly, duplicate chunks, reassembly timeout eviction |
| `internal/policy/policy_test.go` | policy | Allowed domain, blocked domain, private IP block (4 ranges), port block (block + allow), oversized response, wildcard domain, empty allow list |
| `internal/ratelimit/token_bucket_test.go` | ratelimit | Burst exhaustion, RPS refill, adaptive On429 reduction, backoff duration range, On429 floor |
| `tests/integration/memory_relay_test.go` | integration | GET relay, POST relay, 1 MB response relay |

**All tests pass, including `go test -race ./...`.**

### Missing tests (critical before MVP)

| Area | Missing test |
|---|---|
| `internal/compress` | No tests at all; gzip compress/decompress round-trip, decompression bomb protection |
| `internal/config` | No tests; `LoadFile` parsing, missing file error, `Config.Validate()` (not implemented yet) |
| `internal/relay` | No tests; session request/response correlation, timeout, concurrent requests |
| `internal/proxy` | No tests; CONNECT rejection, policy deny → 403, body truncation at 10 MB |
| `internal/exit` | No tests; policy enforcement on exit side, content-type block, request timeout |
| `internal/transport` | No tests; MemoryTransport close/send/receive, ErrClosed returned after Close, drop simulation |
| Policy — DNS rebinding | Test that a domain resolving to a private IP is blocked (requires custom resolver injection first) |
| Crypto — wrong key in relay | Integration test: start exit with different key, verify proxy returns 502 not panic |
| Rate limit — wired in relay | Test that sends are blocked when limiter is exhausted (once wired) |
| HTTPS CONNECT | Test that `CONNECT` requests return 501 |
| Policy — hop-by-hop stripping | Test that forwarded request does not contain `Connection:` header |

### Weak tests

- `TestMemoryRelayGET`, `TestMemoryRelayPOST`, `TestMemoryRelay1MBResponse` all disable `BlockPrivateRanges`. Policy enforcement is never tested in the actual relay path.
- No integration test verifies that encryption was active (a test with wrong key would confirm it).
- No test for the error path: policy denied, timeout, unreachable upstream.
- `TestRPSRefill` uses `time.Sleep(200ms)` — this is a time-dependent test that will occasionally flake under load.

### Recommended tests before release

- Fuzz test for `protocol.DecodeMessage` (malformed input of arbitrary length)
- Load test with 100 concurrent requests over memory transport
- Chaos test: MemoryTransport with `DropRate: 0.3` — verify eventual delivery or clean error
- MAX transport integration test with HTTP mock server responding with 200 then 429 then 200

---

## 9. Documentation Review

| Document | Status | Notes |
|---|---|---|
| `README.md` | ACCURATE | Correctly describes architecture, quick start, security properties. Does not overclaim MAX transport. Example configs are in the repo. |
| `docs/architecture.md` | ACCURATE | Package table and data flow description match the code exactly. |
| `docs/protocol.md` | PARTIAL / OVERCLAIMS | Lists all 9 frame types as if they are part of the wire protocol. Eight of the nine (HELLO, OPEN, ACK, WINDOW, CLOSE, PING, PONG, ERROR) are defined but never sent or processed. Should state which are implemented. |
| `docs/rate-limits.md` | OVERCLAIMS | Describes ACK window, sliding window, and per-type priority as configured and active behaviour. None of it is wired into the relay path. The config fields exist, but reading them has no effect. Must be annotated as "not yet implemented". |
| `docs/max-transport.md` | HONEST | Explicitly states the transport "compiles but returns `ErrNotImplemented`". Good. |
| `docs/security.md` | PARTIAL | Honest about replay protection gap, padding, and forward secrecy. Does not mention the DNS rebinding vulnerability in the exit node, which is the most exploitable current risk. |
| Example configs | ACCURATE | Syntax and values are valid. The salt `saltchangeme1234` is a placeholder that looks like a real value — consider using an obvious placeholder like `CHANGE_ME_TO_RANDOM_16BYTES` or adding a startup warning. |

---

## 10. MVP Completion Plan

### Phase A — Make it honest and runnable

- [ ] **A1.** Add `fmt` and `test-race` tasks to `Taskfile.yml`; add a CI-suitable format check step.
- [ ] **A2.** Add `Config.Validate()` in `internal/config`; call it in `runClient` and `runExit` before `deriveKey`. Validate: salt exactly 16 bytes, passphrase_env non-empty, argon2 params > 0, transport type known.
- [ ] **A3.** Add `case "memory"` to `buildTransport` so developers can run `client` and `exit` modes locally without MAX credentials.
- [ ] **A4.** Annotate `docs/rate-limits.md` with "not yet wired in relay path" notices for ACK, sliding window, and retry sections.
- [ ] **A5.** Add DNS rebinding section to `docs/security.md`.
- [ ] **A6.** Remove `protocol/envelope.go` (dead code — `Envelope` struct is never used).
- [ ] **A7.** Add `CHUNKBRIDGE_SHARED_KEY` startup check with clear message if not set when running `selftest` from binary (currently crashes with confusing error if run without the env var in built binary form).

### Phase B — Make relay real

- [ ] **B1.** Wire `AdaptiveRateLimiter` into `relay.Session.sendFrame`; check `AllowData()` before each chunk send; block (with backoff) if not allowed.
- [ ] **B2.** Fix DNS rebinding: replace the exit executor's `http.Client` with one using a custom `net.Dialer` that calls `isPrivateIP` on each resolved address. Add `fe80::/10` and `100.64.0.0/10` to the blocklist.
- [ ] **B3.** Strip hop-by-hop request headers in exit executor before making outbound call.
- [ ] **B4.** Add integration test with wrong key to confirm encryption is active in relay path.
- [ ] **B5.** Add integration tests for policy denial (domain block, port block, private IP) that verify a 403 reaches the proxy client.
- [ ] **B6.** Remove or mark as not-implemented: `Frame.StreamID`, `FrameHELLO`, `FrameOPEN`, `FramePING`, `FramePONG`, `FrameWINDOW`. Keep `FrameACK`, `FrameCLOSE`, `FrameERROR` — they are needed for Phase C.

### Phase C — Make it robust

- [ ] **C1.** Implement `FrameACK` sending: exit node sends ACK after processing each request; session layer tracks unACKed chunks.
- [ ] **C2.** Implement `FrameERROR`: exit node sends an ERROR frame for policy/timeout failures instead of a fake HTTP response; proxy decodes ERROR and returns appropriate HTTP status.
- [ ] **C3.** Implement `FrameCLOSE`: clean stream teardown; proxy sends CLOSE after response is consumed; exit node stops accepting chunks for that request ID.
- [ ] **C4.** Implement sliding window in `relay.Session`: limit in-flight chunks to `WindowConfig.InitialSize`; advance on ACK; apply AIMD (shrink on 429/timeout, grow on clean ACK).
- [ ] **C5.** Add concurrent request limit in `relay.Session` to prevent memory exhaustion.
- [ ] **C6.** Add graceful shutdown: `signal.NotifyContext` in `runClient`/`runExit`; call `http.Server.Shutdown(ctx)` when signal received.
- [ ] **C7.** Implement retry with bounded attempts (`AckConfig.MaxRetries`) and jitter backoff when a chunk is not ACKed within `AckConfig.TimeoutMs`.
- [ ] **C8.** Add missing tests for `compress`, `config`, `relay`, `proxy`, `exit`, `transport` packages.
- [ ] **C9.** Expose metrics: add a minimal `/debug/metrics` HTTP handler on the listen address returning counter values as plain text.

### Phase D — Prepare MAX transport

- [ ] **D1.** Implement `MaxTransport.Send`: POST message to MAX Bot API send endpoint with `Authorization: Bearer <token>`.
- [ ] **D2.** Implement `MaxTransport.Receive`: long-poll inbox endpoint every `PollMs`; deduplicate re-delivered messages by message ID; deliver on channel.
- [ ] **D3.** Wire `AdaptiveRateLimiter.On429()` and `BackoffDuration()` into `MaxTransport.Send` on 429 response.
- [ ] **D4.** Enforce encoded message size ≤ `MaxB64Chars` before each send; return error if exceeded (chunking should prevent this, but verify).
- [ ] **D5.** Add integration test against an `httptest.Server` mock MAX API; test 200 send, 429 backoff, and polling receive.

---

## 11. Release-Quality Roadmap

The following are post-MVP items. Do not conflate with MVP readiness.

- **Real MAX integration**: Complete Phase D above, then test against the live MAX Bot API in a staging environment. Document polling rate limitations and webhook alternative.
- **Production config validation**: Add JSON Schema or strict YAML validation for all config fields; provide a `chunkbridge validate-config <file>` subcommand.
- **Structured metrics endpoint**: Expose Prometheus-compatible `/metrics` for operational monitoring. Include rate-limit bucket states, queue depth, reassembly in-progress count.
- **System service examples**: Provide `systemd` unit files and example configs with correct file permissions (config should be 0600, owned by the service user).
- **Signed binaries / checksums**: Add `goreleaser` configuration for reproducible, signed releases with `sha256` checksums.
- **CI workflow**: GitHub Actions workflow: `gofmt` check, `go vet`, `go test ./...`, `go test -race ./...`, `go build`, binary size tracking.
- **Fuzz tests**: `FuzzDecodeMessage` targeting the protocol decoder with arbitrary input; `FuzzDecompress` for gzip bomb protection.
- **Load tests**: Measure throughput and memory at 10/50/100 concurrent relay requests; establish regressions baselines.
- **Chaos tests**: `MemoryTransport` with `DropRate > 0` and verify retry+ACK path recovers correctly; simulate late delivery of out-of-order chunks.
- **Forward secrecy**: Implement ephemeral per-session key exchange (e.g., X25519 ECDH) so that compromising the long-term passphrase does not retroactively decrypt past sessions.
- **Padding**: Add random payload padding to resist traffic-analysis attacks that infer request/response size from message count.
- **Security hardening**: Verify no `InsecureSkipVerify` path is possible; add `Content-Security-Policy` to the proxy's error responses; add timeouts to all network operations including DNS resolution.
- **Documentation cleanup**: Full API reference for the config format; operator runbook covering key rotation, salt regeneration, and incident response.

---

## 12. Recommended Next PRs

### PR 1 — `fix: Taskfile tasks, dead code cleanup, and docs honesty`
**Goal:** Add missing workflow tasks, remove dead code that implies unimplemented functionality, and annotate documentation that overclaims active behaviour.  
**Files likely affected:**
- `internal/protocol/envelope.go` (remove)
- `Taskfile.yml` (add fmt, test-race tasks)
- `docs/rate-limits.md` (add "not yet wired" notices)
- `docs/security.md` (add DNS rebinding section)

**Acceptance criteria:**
- `task fmt`, `task test-race` both exist and succeed
- `internal/protocol/envelope.go` deleted; `go build ./...` still passes
- `docs/rate-limits.md` does not claim active ACK/window/retry behaviour
- `docs/security.md` documents the DNS rebinding risk

**Tests to add:**
- None required; dead-code removal and documentation corrections only.

---

### PR 2 — `feat: config validation and memory transport in CLI`
**Goal:** Fail fast at startup with clear errors; allow local development without MAX credentials.  
**Files likely affected:**
- `internal/config/config.go` (add `Validate() error`)
- `internal/config/` (add `config_test.go`)
- `cmd/chunkbridge/main.go` (call `cfg.Validate()`, add `case "memory"` in `buildTransport`)

**Acceptance criteria:**
- `chunkbridge client` with missing salt prints `config: crypto.salt must be set` before any other error
- `chunkbridge client` with a memory transport config starts and accepts connections
- All invalid config combinations return clear startup errors

**Tests to add:**
- `TestConfigValidate_MissingSalt`
- `TestConfigValidate_MissingPassphraseEnv`
- `TestConfigValidate_ZeroArgon2Params`
- `TestConfigValidate_UnknownTransport`

---

### PR 3 — `fix: DNS rebinding protection and hop-by-hop header stripping`
**Goal:** Close the most significant security gap before any production use.  
**Files likely affected:**
- `internal/policy/policy.go` (add `fe80::/10`, `100.64.0.0/10` to private ranges)
- `internal/exit/http_executor.go` (custom dialer with post-resolve IP check; strip hop-by-hop headers)
- `docs/security.md` (document the fix)

**Acceptance criteria:**
- A request to a hostname that DNS-resolves to `192.168.1.1` is rejected with 403 when `BlockPrivateRanges = true`
- `Connection`, `Transfer-Encoding`, `TE`, `Trailers`, `Upgrade` headers are not forwarded to upstream
- `fe80::1` and `100.64.0.1` are rejected as private IPs

**Tests to add:**
- `TestDNSRebindingBlocked` (requires custom resolver stub)
- `TestHopByHopHeadersStripped`
- `TestPrivateIPv6LinkLocalBlocked`
- `TestCGNATBlocked`

---

### PR 4 — `feat: wire rate limiter into relay session`
**Goal:** Ensure the implemented rate limiter actually throttles sends.  
**Files likely affected:**
- `internal/relay/session.go` (import ratelimit, add limiter field, check before send)
- `internal/proxy/http_proxy.go` (pass limiter config from config.Limits)
- `internal/exit/http_executor.go` (same)

**Acceptance criteria:**
- Sends from `relay.Session.sendFrame` call `rl.AllowData()` before each chunk
- When limiter is exhausted, send waits (not drops) up to context deadline
- `On429()` path is reachable from transport error handling (even if MAX is a stub)

**Tests to add:**
- `TestSessionRateLimited` — verify sends block when bucket is empty
- `TestSessionAdaptiveBackoff` — verify On429 reduces rate and respects backoff delay
- `TestAdaptiveRateLimiter429Integration` — integration test simulating 429 response

---

### PR 5 — `feat: ACK and CLOSE frames in session layer`
**Goal:** Implement reliable delivery acknowledgement so the relay has a basis for retry and flow control.  
**Files likely affected:**
- `internal/relay/session.go` (send ACK after each received response; track pending ACKs)
- `internal/exit/http_executor.go` (send ACK after decoding each request chunk; handle CLOSE)
- `internal/protocol/ack.go` (already exists, just needs to be wired)
- New file: `internal/relay/session_test.go`

**Acceptance criteria:**
- Exit node sends `FrameACK` after processing each DATA chunk
- Session layer updates acknowledged sequence counter on receipt of ACK
- Session layer sends `FrameCLOSE` when request/response cycle completes
- Duplicate ACK received does not panic or reset state

**Tests to add:**
- `TestSessionACKAdvancesSequence`
- `TestSessionCLOSEDeallocatesPending`
- `TestDuplicateACKIsIdempotent`

---

### PR 6 — `feat: error frame propagation and graceful shutdown`
**Goal:** Propagate fatal errors over the wire rather than silently discarding them; ensure the binary shuts down cleanly on SIGTERM/SIGINT.  
**Files likely affected:**
- `internal/exit/http_executor.go` (send FrameERROR instead of relayResponse with 4xx/5xx)
- `internal/relay/session.go` (handle FrameERROR in dispatch; return error to caller)
- `cmd/chunkbridge/main.go` (add signal handler and `http.Server.Shutdown`)

**Acceptance criteria:**
- Policy denial at exit sends `FrameERROR`; proxy caller receives a 403 status, not a 502 from JSON-decode failure
- `SIGTERM` causes the proxy to stop accepting new connections, drain in-flight requests, then exit 0
- `SIGTERM` causes the exit node to stop receiving, drain in-flight requests, then exit 0

**Tests to add:**
- `TestExitPolicyErrorPropagation` — integration test verifying 403 reaches proxy for a blocked domain
- `TestGracefulShutdown` — verify that in-flight request completes after shutdown signal
