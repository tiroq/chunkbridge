# Rate Limits

## Current Implementation Status

The table below reflects what is actually wired into the relay path today versus what is planned.

| Feature | Status |
|---------|--------|
| `TokenBucket` implementation | **Implemented** — unit-tested in `internal/ratelimit` |
| `AdaptiveRateLimiter` (429 halving, backoff) | **Implemented** — unit-tested in `internal/ratelimit` |
| Rate limiter wired into `relay.Session` send path | **Implemented** — every outbound DATA chunk waits for `AllowData()` before `Transport.Send` |
| Rate limiter wired into `exit.HTTPExecutor` send path | **Implemented** — every outbound response chunk waits for `AllowData()` before `Transport.Send` |
| Rate limiter built from config in CLI | **Implemented** — `buildRateLimiter` in `cmd/chunkbridge/main.go` creates an `AdaptiveRateLimiter` from `cfg.Limits` and passes it to both proxy session and exit executor |
| Bounded concurrent pending requests (`proxy.max_concurrent_requests`) | **Implemented** — default 64; overflow returns `relay: too many concurrent requests` → HTTP 429 |
| Per-request relay timeout (`proxy.request_timeout_ms`) | **Implemented** — default 30 000 ms; timeout returns HTTP 502 Bad Gateway |
| ACK frames (`FrameACK`) | **Defined but not wired** — `NewACKFrame`/`IsACK` exist in `internal/protocol/ack.go` but are never called from session, relay, or exit |
| WINDOW frames and sliding-window flow control | **Not implemented** — `WindowConfig` struct exists in config; no sliding-window logic exists in the relay path |
| Retry-After / 429 handling in MAX transport | **Implemented (mocked only)** — `MaxTransport` now calls `On429()` and returns `*RateLimitError` with the parsed `Retry-After` duration on HTTP 429. Wire `WithOn429(lim.On429)` at startup. The transport does not automatically sleep-and-retry sends; the caller receives a `*RateLimitError` and must decide whether to retry. Tested against `httptest.Server`; not validated against the live MAX API. |
| Control vs. data priority queues | **Not implemented** — all sends share one transport channel with no priority |

> **Summary for operators:** DATA sends in `relay.Session` and `exit.HTTPExecutor` are throttled by the configured rate limiter. Concurrent in-flight requests are capped at `proxy.max_concurrent_requests` (default 64) with a 429 response on overflow. Per-request timeouts are enforced via `proxy.request_timeout_ms` (default 30 s). ACK, window, retry, and priority-queue features remain unimplemented.

---

chunkbridge uses a three-bucket token-bucket system to avoid overwhelming the underlying message platform.

## Buckets

| Bucket | Default RPS | Purpose |
|--------|-------------|---------|
| `global` | 5 | Overall message send rate |
| `data` | 4 | Data frame sends |
| `control` | 2 | ACK / PING / PONG frames |

All sends check the `global` bucket **and** the appropriate per-type bucket. Both must have capacity.

## Adaptive Behaviour on 429 Responses (Partially wired — 429 feedback not yet connected)

> **Note:** `AdaptiveRateLimiter` is implemented, unit-tested, and now called from the relay send path. However, `On429()` is **not yet called** from the MAX transport or any other component — the 429 halving / backoff logic exists but is not triggered at runtime. This will be wired when MAX transport is fully implemented.

When the transport layer receives a 429 (Too Many Requests) error:

1. `AdaptiveRateLimiter.On429()` is called.
2. `dataRPS` is halved (floor: 0.5 RPS).
3. The data token bucket is rebuilt with the new rate and burst=1.
4. The caller waits for `BackoffDuration()` = 1s + up to 500ms jitter.

## Configuration

> **Note:** The `ack` and `window` config fields are parsed but have **no runtime effect** in the current version. The `rate_limits` fields (`global_rps`, `data_rps`, `control_rps`, `burst`) **are now used** to build the runtime limiter in client and exit mode.

```yaml
rate_limits:
  global_rps: 5
  data_rps: 4
  control_rps: 2
  burst: 10
  message:
    max_chars: 4000      # hard limit from platform
    safe_chars: 3600     # chunkbridge self-imposed limit
    max_b64_chars: 3400  # budget for encrypted payload
  ack:
    interval_ms: 500
    timeout_ms: 5000
    max_retries: 5
  window:
    initial_size: 4
    max_size: 16
    min_size: 1
```
