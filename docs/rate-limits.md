# Rate Limits

## Current Implementation Status

The table below reflects what is actually wired into the relay path today versus what is planned.

| Feature | Status |
|---------|--------|
| `TokenBucket` implementation | **Implemented** — unit-tested in `internal/ratelimit` |
| `AdaptiveRateLimiter` (429 halving, backoff) | **Implemented** — unit-tested in `internal/ratelimit` |
| Rate limiter wired into `relay.Session` hot path | **Not implemented yet** — no calls to `AllowData()` or `On429()` exist in relay, proxy, or exit |
| ACK frames (`FrameACK`) | **Defined but not wired** — `NewACKFrame`/`IsACK` exist in `internal/protocol/ack.go` but are never called from session, relay, or exit |
| WINDOW frames and sliding-window flow control | **Not implemented** — `WindowConfig` struct exists in config; no sliding-window logic exists in the relay path |
| Retry-After / 429 handling in MAX transport | **Not implemented** — `BackoffDuration()` exists in `AdaptiveRateLimiter` but is never called from the transport layer |
| Control vs. data priority queues | **Not implemented** — all sends share one transport channel with no priority |

> **Summary for operators:** The rate-limiting config fields (`ack`, `window`, `rate_limits`) are parsed and stored but have **no runtime effect** in the current version. All relay traffic is sent at memory speed with no throttling. These features are planned for a future release.

---

chunkbridge uses a three-bucket token-bucket system to avoid overwhelming the underlying message platform.

## Buckets

| Bucket | Default RPS | Purpose |
|--------|-------------|---------|
| `global` | 5 | Overall message send rate |
| `data` | 4 | Data frame sends |
| `control` | 2 | ACK / PING / PONG frames |

All sends check the `global` bucket **and** the appropriate per-type bucket. Both must have capacity.

## Adaptive Behaviour on 429 Responses (Planned — not yet wired into relay path)

> **Note:** `AdaptiveRateLimiter` is implemented and unit-tested, but it is **not yet called** from the MAX transport or relay session. The behaviour below describes the intended design once it is wired in.

When the transport layer receives a 429 (Too Many Requests) error:

1. `AdaptiveRateLimiter.On429()` is called.
2. `dataRPS` is halved (floor: 0.5 RPS).
3. The data token bucket is rebuilt with the new rate and burst=1.
4. The caller waits for `BackoffDuration()` = 1s + up to 500ms jitter.

## Configuration

> **Note:** The `ack`, `window`, and `rate_limits` config fields are parsed but have **no runtime effect** in the current version. They represent the target configuration schema for planned features.

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
