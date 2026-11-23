# Internal metrics

Git Messages exposes Prometheus-compatible operational metrics when `server.metrics.enabled` is enabled. The metrics routes are internal operator endpoints. Restrict them at the firewall or reverse proxy so only the monitoring network can reach them.

## Endpoints

All routes accept `GET` and require a service-specific bearer token in the `Authorization` header. Tokens are generated on first configuration load and stored in the owner-only `secrets.yml` file. Query-string tokens are rejected.

| Endpoint | Token | Response |
|---|---|---|
| `/server/metrics` and `/server/metrics/prometheus` | `server.metrics.auth.tokens.prometheus` | Prometheus text exposition |
| `/server/metrics/grafana` | `server.metrics.auth.tokens.grafana` | Importable Grafana dashboard JSON |
| `/server/metrics/loki` | `server.metrics.auth.tokens.loki` | Recent request events in Loki push-stream JSON |
| `/api/{api_version}/server/metrics[/{service}]` | Per service | Versioned aliases |
| `/api/metrics[/{service}]` | Per service | Unversioned aliases |
| `/metrics[/{service}]` | Per service | Root aliases, controlled by `server.metrics.root.enabled` |

The aliases invoke the same handler and do not redirect. The Prometheus endpoint includes application uptime/build information, HTTP request count, request duration and size, active requests, rate-limit totals, authentication attempts for metrics, operator, and owner tokens, live counts of active primary and linked owner tokens, successful commit-message builds, Tor status, database queries/errors/connection pool use, scheduler executions/duration/running state, Go runtime values, and host memory, CPU, and data-directory disk values on supported platforms. Authentication labels use only the bounded method and `success`/`failed` status. Database labels contain only the SQL operation, table, and bounded error type; SQL text and parameters are never exported. SQLite lock contention lasting over one second increments `gitmessages_db_busy_total`, observes the write wait in `gitmessages_db_write_wait_seconds`, and logs `db.busy` with the wait duration and caller. Scheduler labels use registered task IDs. The Loki route serves bounded application log lines and request events; credentials are redacted, and request events omit headers, query strings, and client IP addresses.

Database metrics use `gitmessages_db_queries_total{operation,table}`, `gitmessages_db_query_duration_seconds{operation,table}`, `gitmessages_db_connections_open`, `gitmessages_db_connections_in_use`, and `gitmessages_db_errors_total{operation,error_type}`. SQLite contention metrics are `gitmessages_db_busy_total` and `gitmessages_db_write_wait_seconds`. Operations are `select`, `insert`, `update`, and `delete`; error types are `connection`, `timeout`, `constraint`, `duplicate`, or `other`.

Scheduler metrics use `gitmessages_scheduler_tasks_total{task,status}`, `gitmessages_scheduler_task_duration_seconds{task}`, `gitmessages_scheduler_tasks_running{task}`, and `gitmessages_scheduler_last_run_timestamp{task}`. Status is `success` or `error`; task labels are built-in scheduler task IDs.

The business metric `gitmessages_commit_message_builds_total` counts successful stateless message builds from the browser and API. The rate-limit counter backend exports `gitmessages_cache_hits_total`, `gitmessages_cache_misses_total`, `gitmessages_cache_evictions_total`, `gitmessages_cache_size`, and `gitmessages_cache_bytes`, each labelled with the bounded cache purpose (`rate_limit`). Item and byte gauges are process-local estimates, including for the in-memory backend. The Grafana dashboard includes HTTP, database, cache, scheduler, system, and message-build panels.

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
