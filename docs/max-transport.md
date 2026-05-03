# Max Transport

`internal/transport/maxapi.go` contains a skeleton `MaxTransport` that will integrate with the Max.ai (or compatible) messaging API.

## Current Status

The transport **compiles** and satisfies the `Transport` interface but returns `ErrNotImplemented` for `Send` and `Receive`. It is a placeholder until the API endpoints are published.

## Configuration

```yaml
transport:
  type: max
  max:
    token_env: MAX_API_TOKEN   # env var holding the bearer token
    from_handle: "@my-agent"
    to_handle: "@exit-agent"
    poll_ms: 1000
```

The bearer token is read from the environment variable named by `token_env` at startup. It is **never** logged or included in error messages.

## Implementation Notes (TODO)

When wiring up the real API:

1. `Send`: POST the encoded message text to the send endpoint with `Authorization: Bearer <token>`.
2. `Receive`: poll the inbox endpoint every `poll_ms` ms (or set up a webhook) and deliver new messages on the channel.
3. Handle 429 by calling `AdaptiveRateLimiter.On429()` before returning.
4. Handle message de-duplication (the API may re-deliver messages).
5. Respect `MemoryOptions`-style latency simulation in integration tests by using `MemoryTransport` with a matching option.

## Security

* The token is stored in memory only; it is read once at startup from the environment.
* TLS verification is always enabled for outbound API calls (no `InsecureSkipVerify`).
