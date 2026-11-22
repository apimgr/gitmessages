# Internal metrics

Git Messages exposes Prometheus-compatible operational metrics when `server.metrics.enabled` is enabled. The metrics routes are internal operator endpoints. Restrict them at the firewall or reverse proxy so only the monitoring network can reach them.

## Endpoints

All routes accept `GET` and require a service-specific bearer token in the `Authorization` header. Tokens are generated on first configuration load and stored in the owner-only `secrets.yml` file. Query-string tokens are rejected.

| Endpoint | Token | Response |
|---|---|---|
| `/server/metrics` and `/server/metrics/prometheus` | `server.metrics.auth.tokens.prometheus` | Prometheus text exposition |
| `/server/metrics/grafana` | `server.metrics.auth.tokens.grafana` | Importable Grafana dashboard JSON |
| `/server/metrics/loki` | `server.metrics.auth.tokens.loki` | Recent request events in Loki push-stream JSON |
| `/api/v1/server/metrics[/{service}]` | Per service | Versioned aliases |
| `/api/metrics[/{service}]` | Per service | Unversioned aliases |
| `/metrics[/{service}]` | Per service | Root aliases, controlled by `server.metrics.root.enabled` |

The aliases invoke the same handler and do not redirect. The Prometheus endpoint includes application uptime/build information, HTTP request count, request duration and size, active requests, rate-limit totals, metrics-token authentication attempts, zero-valued owner-token gauges, Tor status, Go runtime values, and host memory, CPU, and data-directory disk values on supported platforms. Database, cache, and scheduler metrics are not emitted because Git Messages has none of those systems. The Loki route serves bounded application log lines and request events; credentials are redacted, and request events omit headers, query strings, and client IP addresses.

## Configuration

Metrics are disabled by default. Enable them in `yaml.d/metrics.yml`:

```yaml
server:
  metrics:
    enabled: true
    root:
      enabled: true
    auth:
      allow_unauthenticated: false
    include_system: true
    include_runtime: true
    loki:
      max_entries: 1000
      max_age: 1h
```

Each service uses an independent token. Configuration loading generates missing tokens and persists them under `server.metrics.auth.tokens` in `secrets.yml`; do not copy those values into public configuration or source control. Setting `allow_unauthenticated: true` bypasses token checks and is suitable only when network controls already restrict access.

## Prometheus scrape example

```yaml
scrape_configs:
  - job_name: gitmessages
    metrics_path: /metrics
    static_configs:
      - targets: ["gitmessages.internal:8080"]
    authorization:
      credentials_file: /run/secrets/gitmessages-prometheus-token
```

Keep credentials in a secret file or secret manager. Do not put tokens in URLs, logs, or checked-in scrape configuration.
