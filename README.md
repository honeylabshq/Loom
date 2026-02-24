# Loom

**Loom** is an enrichment service for ECS-formatted log events. It receives batched events over HTTPS (e.g. from honeypot sensors), enriches each event with ASN, GEO, and optional DNS using local or cached data, and writes the enriched ECS to stdout, [ClickHouse](https://clickhouse.com/), or Elasticsearch.

**Version:** 1.x

## What it does

1. **Ingest** — Accepts `POST` requests with a JSON array of ECS events. Validates a Bearer token and optional sensor id header; applies per-sensor rate limits.
2. **Enrich** — For each event with a source IP: looks up ASN and GeoIP (MaxMind GeoLite2) and optionally reverse DNS (PTR), then adds `source.as`, `source.geo`, and `source.domain` to the event. Preserves all other fields.
3. **Output** — Writes one enriched event per destination: stdout (one JSON line per event), ClickHouse (HTTP INSERT), or Elasticsearch (bulk API). ClickHouse is checked at startup; each flush is logged. Optional disk outbox can spool failed ClickHouse batches and retry.

Configuration is TOML-based. Secrets (tokens, DB credentials) are supplied via environment or token file, not the config file or CLI.

## How it works

- **Clients** (e.g. [Spip](https://github.com/StefanGrimminck/Spip-Go) sensors) send batches of ECS events to Loom’s ingest endpoint. Each request must include `Authorization: Bearer <token>` and optionally `X-Spip-ID: <sensor_id>` (or equivalent); the token maps to a single sensor id.
- **Loom** validates the token, rate-limits per sensor, parses the JSON array, enriches each event (if MaxMind DBs and/or DNS are configured), and writes to the configured output. For ClickHouse, events are buffered and flushed every 100 events or every configured flush interval; each flush is logged. With outbox enabled, failed ClickHouse inserts are persisted to local disk and drained automatically.
- **Output** can be stdout (for debugging or piping), ClickHouse (table must have an `event` String column), or Elasticsearch. Health and Prometheus metrics are exposed on a separate management port.

For a step-by-step deployment guide (one Loom + one sensor), see [docs/SETUP_GUIDE.md](docs/SETUP_GUIDE.md).

## Build

```bash
go build -o loom ./cmd/loom
```

**Docker:** `docker build -t loom:latest .` — see [docs/DOCKER.md](docs/DOCKER.md) for run options, Compose, and security notes.

## Quick start

1. **Copy and edit config**

   ```bash
   cp loom.example.toml loom.toml
   ```

   Loom does not accept secrets on the CLI. Provide tokens via environment (one per sensor):

   ```bash
   export LOOM_SENSOR_my-sensor="your-secret-token"
   ```

   Or use `auth.token_file` with one line per `token,sensor_id`.

2. **Development (no TLS)**  
   In `loom.toml` set `server.tls = false` and leave `cert_file` / `key_file` empty. Use `output.type = "stdout"`.

3. **Enrichment (optional)**  
   Download [MaxMind GeoLite2](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data) (City + ASN) and set `enrichment.geoip_db_path` and `enrichment.asn_db_path`. If omitted, Loom still runs and forwards events without ASN/GEO.

4. **Run**

   ```bash
   ./loom -config loom.toml
   ```

## Ingest API

- **Endpoints:** `POST /api/v1/ingest`, `POST /ingest`, or `POST /` (all equivalent).
- **Transport:** HTTPS in production (TLS 1.2+); HTTP only for local development.
- **Headers:** `Authorization: Bearer <token>` (required); `X-Spip-ID` (sensor id; must match the token’s sensor).
- **Body:** JSON array of ECS event objects.

Response codes: 200/204 success; 400 invalid request; 401 unauthorized; 413 payload or batch too large; 429 rate limit; 500/503 server errors.

## Health and metrics

- **Liveness:** `GET /health` or `GET /live` on the management port → 200 when the process is running.
- **Readiness:** `GET /ready` → 200 when the service can accept ingest and use output; 503 otherwise.
- **Metrics:** `GET /metrics` (Prometheus) when `observability.metrics_enabled = true`.

Management port is set by `server.management_listen_address` (e.g. `:9080`).

## Configuration summary

| Area         | Key options |
|-------------|-------------|
| **Server**  | `listen_address`, `tls`, `cert_file`, `key_file`, `management_listen_address` |
| **Auth**     | `token_file` or env `LOOM_SENSOR_<sensor_id>=<token>` (one token per sensor) |
| **Limits**   | `max_body_size_bytes`, `max_events_per_batch`, `max_event_size_bytes`, `per_sensor_rps` |
| **Enrichment** | `geoip_db_path`, `asn_db_path`, `enrichment.dns.*` |
| **Output**   | `type`: `stdout`, `clickhouse`, or `elasticsearch`; ClickHouse/ES options and env credentials (see example). For ClickHouse, optional `output.outbox.*` enables local disk spooling and retry on DB failures. |
| **Logging**  | `level`, `format` (json or console) |

## Deployment

- Run as a non-root user with minimal privileges.
- Store TLS certs and tokens in a secrets manager or restricted files; do not log tokens or full request/response bodies.
- For horizontal scaling, run multiple Loom instances behind a load balancer; ingest is stateless (caches such as DNS are per-process).

## Production checklist

| Item | Action |
|------|--------|
| **TLS** | Set `server.tls = true` and valid `cert_file` / `key_file`; startup fails if files are missing or unreadable. |
| **Secrets** | Use env `LOOM_SENSOR_*` or restricted `auth.token_file`; never in config or CLI. |
| **Limits** | Tune `max_body_size_bytes`, `max_events_per_batch`, `per_sensor_rps` for your load. |
| **Health** | Expose `management_listen_address` and use `/health` and `/ready` for orchestration. |
| **Metrics** | Enable `observability.metrics_enabled` and scrape `/metrics`. |
| **Logging** | Use `format = "json"` and level `info` or `warn`; avoid logging request bodies or tokens. |
| **Output** | For ClickHouse/Elasticsearch, use TLS where possible and credentials from env. |
| **Outbox** | For ClickHouse production, enable `output.outbox.enabled = true` with a persistent disk path (`output.outbox.dir`) and set queue limits (`max_bytes`). |

See [docs/SETUP_GUIDE.md](docs/SETUP_GUIDE.md) for full deployment and troubleshooting.

## License

See repository license.
