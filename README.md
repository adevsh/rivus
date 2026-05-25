# rivus

Stdlib-only HTTP reverse proxy and load balancer in Go.

> **No auth, no tracing, no retries, no CORS.** By design — pair with an
> identity-aware proxy at the edge, or deploy in a trusted network.

---

## Features

- **Longest-prefix routing** — requests are dispatched to the upstream whose prefix is the longest match of the request path
- **Path rewriting** — strip prefix, replace prefix, or full regex rewrite per upstream
- **Round-robin and least-connections balancing** across multiple backends per upstream
- **Per-IP token-bucket rate limiting** and per-backend throughput cap
- **Three-state circuit breaker** (closed → open → half-open) per backend, with configurable failure threshold and cooldown
- **Periodic health checks** — backends are marked unhealthy on probe failure and excluded from routing until they recover
- **JSON `/metrics` endpoint** — uptime, request counters, per-backend health and connection stats, circuit-breaker state, rate-limiter totals
- **TLS termination** — server-side TLS with cert + key files; minimum TLS 1.2
- **Structured request logs** via `log/slog` (JSON in production, text in dev)
- **Zero external dependencies** — Go standard library only

---

## Install / build

Requires Go 1.24+.

```bash
git clone https://github.com/adevsh/rivus
cd rivus
make build          # produces ./bin/rivus
```

---

## Quick start

A demo with four Bun backend servers:

```bash
make upstream-up    # starts 4 demo backends on ports 9001, 9002, 9011, 9012
make demo-rivus     # runs ./bin/rivus --config config.example.json
```

Then:

```bash
curl http://localhost:8080/api
curl http://localhost:8080/static/file.css   # rewritten to /assets/file.css upstream
curl http://localhost:8080/metrics
```

Stop demo backends: `make upstream-down`.

---

## Configuration

### Loading

```bash
./bin/rivus --config path/to/config.json   # default: config.json
```

Set `RIVUS_ENV=production` to emit logs as structured JSON (default is human-readable text).

### Full schema

See [`config.example.json`](config.example.json) for a complete example.

| Field | Type | Description |
|---|---|---|
| `listen` | `string` | TCP address to bind (e.g. `":8080"`). **Required.** |
| `tls.enabled` | `bool` | Enable TLS termination. Requires `cert_file` and `key_file`. |
| `tls.cert_file` | `string` | Path to PEM certificate file. |
| `tls.key_file` | `string` | Path to PEM private key file. |
| `transport.max_idle_conns` | `int` | Max idle keep-alive connections across all backends. |
| `transport.dial_timeout_seconds` | `int` | Per-backend TCP dial timeout. |
| `transport.response_header_timeout_seconds` | `int` | Max wait for the first response byte from a backend. |
| `transport.idle_conn_timeout_seconds` | `int` | How long an idle keep-alive connection stays open. |
| `features.rate_limiter` | `bool` | Enable per-IP and per-backend token-bucket rate limiting. |
| `features.circuit_breaker` | `bool` | Enable per-backend three-state circuit breaker. |
| `features.health_check` | `bool` | Enable periodic backend health probing. |
| `features.metrics` | `bool` | Expose JSON `/metrics` endpoint. |
| `rate_limiter.per_ip.requests_per_second` | `float64` | Per-IP token refill rate. |
| `rate_limiter.per_ip.burst` | `int` | Per-IP burst capacity. |
| `rate_limiter.per_backend.requests_per_second` | `float64` | Per-backend token refill rate. |
| `rate_limiter.per_backend.burst` | `int` | Per-backend burst capacity. |
| `health_check.interval_seconds` | `int` | Seconds between probe runs. |
| `health_check.path` | `string` | Path to GET on each backend (e.g. `"/healthz"`). |
| `health_check.timeout_seconds` | `int` | HTTP timeout per probe. |
| `circuit_breaker.failure_threshold` | `int` | Consecutive failures before the breaker opens. |
| `circuit_breaker.cooldown_seconds` | `int` | Seconds the breaker stays open before entering half-open. |
| `upstreams` | `map[string]upstream` | Routing table. Key is used in logs and metrics. |

#### Upstream fields

| Field | Type | Description |
|---|---|---|
| `prefix` | `string` | Route prefix matched by longest prefix (e.g. `"/api/v1/users"`). |
| `strip_prefix` | `bool` | Strip the matched prefix before forwarding. |
| `replace_prefix` | `string` | Replace the matched prefix with this value. Mutually exclusive with `rewrite`. |
| `rewrite.pattern` | `string` | Go `regexp` pattern matched against the full path. Takes precedence over `replace_prefix` / `strip_prefix`. |
| `rewrite.replacement` | `string` | Replacement string; supports `$1`, `$2` capture groups. |
| `balancer` | `string` | `"round_robin"` or `"least_connections"`. |
| `backends[].url` | `string` | Backend origin including scheme and host (e.g. `"http://svc:8080"`). |

---

## Routing semantics

Each request is dispatched to the upstream whose `prefix` is the **longest match** of the request path. If nothing matches, the client receives `502 Bad Gateway`.

### Path rewrite precedence

Only one rule fires per request, evaluated in this order:

1. **`rewrite`** — regex-replace the full path if the pattern matches.
2. **`replace_prefix`** — replace the first occurrence of `prefix` in the path with this value.
3. **`strip_prefix`** — remove `prefix` from the start of the path.
4. **No change** — path is forwarded unchanged.

---

## Balancer strategies

| Strategy | Behaviour |
|---|---|
| `round_robin` | Distributes requests evenly across healthy backends. |
| `least_connections` | Prefers the backend with the fewest active proxied requests. Better for long-lived or expensive requests. |

Both strategies skip backends that are unhealthy, have an open circuit breaker, or are saturated by the per-backend rate limiter.

---

## Health checks

When `features.health_check` is enabled, rivus runs `GET <backend-url><path>` on every backend every `interval_seconds`. Any 2xx response marks the backend healthy; anything else marks it unhealthy and excludes it from routing until the next successful probe.

The `path` is **global** — all backends across all upstreams share the same probe path.

---

## Rate limiting

When `features.rate_limiter` is enabled:

- **Per-IP** — each client IP gets a token bucket. IP is read from `X-Forwarded-For` (first hop), falling back to `RemoteAddr`. Requests exceeding the limit receive `429 Too Many Requests`.
- **Per-backend** — each backend has its own bucket; saturated backends are excluded by the balancer until the bucket refills.

---

## Circuit breaker

When `features.circuit_breaker` is enabled, each backend has a three-state breaker:

| State | Behaviour |
|---|---|
| **Closed** | Normal operation. |
| **Open** | Backend excluded for `cooldown_seconds`. Requests that would hit it get `503 Service Unavailable`. |
| **Half-open** | One probe request allowed through. Success → closed; failure → open again. |

The breaker opens after `failure_threshold` consecutive failures. Both connection errors and 5xx responses from the backend count as failures.

---

## Metrics

When `features.metrics` is enabled, `GET /metrics` returns a JSON snapshot:

```json
{
  "uptime_seconds": 3600,
  "total_requests": 18423,
  "active_connections": 4,
  "upstreams": {
    "my-service": {
      "backends": [
        {
          "url": "http://svc:8080",
          "healthy": true,
          "active_conns": 2,
          "total_requests": 18423,
          "total_errors": 1,
          "circuit_breaker_state": "closed",
          "circuit_breaker_trips": 0
        }
      ]
    }
  },
  "rate_limiter": {
    "total_limited_requests": 12
  }
}
```

---

## Limitations

- **No authentication or authorization** — JWT tokens, API keys, OAuth, mTLS, and basic auth are not supported.
- **No OpenTelemetry / distributed tracing** — logs only; no spans or trace-context propagation.
- **No automatic retries** — a failed backend request is returned to the client immediately.
- **No CORS** — no `Access-Control-Allow-*` headers are injected.
- **Single global health-check path** — all backends share one probe path; per-upstream paths are not supported.
- **No per-upstream timeouts** — timeout tuning is at the transport level only.
- **Path-prefix routing only** — no Host-header, method, or header-based routing.
- **`X-Forwarded-For` is always overwritten** with the client IP; any inbound value is replaced.

---

## Using rivus as the corporate-ops API gateway

[`config.corporate-ops.json`](config.corporate-ops.json) wires rivus as the
edge HTTP gateway for the corporate-ops monorepo. It routes the
`/api/v1/*` namespace to each microservice running on the `corporate_ops`
Docker network.

### Routing table

| Upstream | Prefix | Backend container |
|---|---|---|
| `platform-core` | `/api/v1/platform-core` | `corporate-ops-platform_core:8080` |
| `people-core` | `/api/v1/people-core` | `corporate-ops-people_core:8080` |
| `hr-talent` | `/api/v1/hr-talent` | `corporate-ops-hr_talent:8080` |
| `payroll` | `/api/v1/payroll` | `corporate-ops-payroll:8080` |
| `accounting` | `/api/v1/accounting` | `corporate-ops-accounting:8080` |
| `fp-and-a` | `/api/v1/fp-and-a` | `corporate-ops-fp_and_a:8080` |
| `treasury` | `/api/v1/treasury` | `corporate-ops-treasury:8080` |
| `expense` | `/api/v1/expense` | `corporate-ops-expense:8080` |
| `crm` | `/api/v1/crm` | `corporate-ops-crm:8080` |
| `erp` | `/api/v1/erp` | `corporate-ops-erp:8080` |

Container hostnames follow the corporate-ops Makefile convention:
`corporate-ops-<SERVICE>` where `SERVICE` is the snake-case service name
(e.g., `platform_core`, `fp_and_a`).

### Running the gateway

Start the corporate-ops infrastructure and at least one service:

```bash
cd /path/to/corporate-ops
make infra-up
make migrate-up SERVICE=platform_core WITH_SEEDS=1
make dev-run SERVICE=platform_core HOST_HTTP_PORT=18080
```

Run rivus on the host (it can reach Docker bridge networks directly):

```bash
cd /path/to/rivus
make build
./bin/rivus --config config.corporate-ops.json
```

Smoke test:

```bash
# Route to platform_core
curl -s http://localhost:8080/api/v1/platform-core/organizations | jq

# Check backend health in metrics
curl -s http://localhost:8080/metrics | jq '.upstreams["platform-core"].backends[0]'
```

### Auth gap

No corporate-ops service implements authentication, and rivus adds none. This
config is suitable for local development and trusted internal networks. Before
exposing to an external network, place an identity-aware proxy (e.g.,
`oauth2-proxy`, Kong, or a custom JWT middleware) in front of rivus, or add
auth middleware directly to the corporate-ops services.

### Unimplemented services

Only `platform_core` is fully implemented today. The other nine services are
scaffolded stubs with no routes. Their upstreams are pre-wired in the config —
when a service is brought up its container becomes reachable and the health
check will mark it healthy automatically. Until then, rivus marks those
backends unhealthy and the circuit breaker opens; no manual config change is
needed.
