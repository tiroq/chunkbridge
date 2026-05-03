# Rate Limits

chunkbridge uses a three-bucket token-bucket system to avoid overwhelming the underlying message platform.

## Buckets

| Bucket | Default RPS | Purpose |
|--------|-------------|---------|
| `global` | 5 | Overall message send rate |
| `data` | 4 | Data frame sends |
| `control` | 2 | ACK / PING / PONG frames |

All sends check the `global` bucket **and** the appropriate per-type bucket. Both must have capacity.

## Adaptive Behaviour (429 Responses)

When the transport layer receives a 429 (Too Many Requests) error:

1. `AdaptiveRateLimiter.On429()` is called.
2. `dataRPS` is halved (floor: 0.5 RPS).
3. The data token bucket is rebuilt with the new rate and burst=1.
4. The caller waits for `BackoffDuration()` = 1s + up to 500ms jitter.

## Configuration

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
