# Loom

Loom is an enrichment service that receives batched ECS log events from [Spip](https://github.com/StefanGrimminck/Spip-Go) honeypot sensors over HTTPS, enriches each event with ASN, GEO, and optional DNS using local or cached data, and outputs enriched ECS events to stdout, [ClickHouse](https://clickhouse.com/), Elasticsearch, or similar.

Configuration is TOML-based; secrets are supplied via environment or token file, not the CLI.

For a step-by-step deployment guide (one Loom + one Spip), see [docs/SETUP_GUIDE.md](docs/SETUP_GUIDE.md).

## Build

```bash
go build -o loom ./cmd/loom
```

**Docker:** `docker build -t loom:latest .` — see [docs/DOCKER.md](docs/DOCKER.md) for run options, Compose, and security notes.

## Quick start

1. **Configuration**

   Copy the example config and set authentication. Loom does not accept secrets on the CLI.

   ```bash
   cp loom.example.toml loom.toml
   ```

   Provide tokens via environment (one token per sensor):

   ```bash
   export LOOM_SENSOR_vps-frankfurt-01="your-secret-token"
   ```

   Or use a token file: set `auth.token_file` to a path with one line per `token,sensor_id`.

2. **Development (no TLS)**

   In `loom.toml`, set `server.tls = false` and leave `cert_file` and `key_file` empty. Use `output.type = "stdout"`.

3. **Enrichment (optional)**

   For ASN and GEO, download [MaxMind GeoLite2](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data) and set `enrichment.geoip_db_path` and `enrichment.asn_db_path`. If omitted, Loom still runs and forwards events without ASN/GEO.

4. **Run**

   ```bash
   ./loom -config loom.toml
   ```

## Ingest API

- **Endpoints:** `POST /api/v1/ingest`, `POST /ingest`, or `POST /` (all equivalent; multiple paths for Spip compatibility).
- **Transport:** HTTPS in production (TLS 1.2+); HTTP only for local development.
- **Headers:**
  - `Authorization: Bearer <token>` — required; constant-time validation.
  - `X-Spip-ID` — sensor identifier; must match the token’s sensor.
- **Body:** JSON array of ECS event objects.

Response codes: 200/204 success; 400 invalid request; 401 unauthorized; 413 payload or batch too large; 429 rate limit; 500/503 server errors.

## Health and metrics

- **Liveness:** `GET /health` or `GET /live` on the management port → 200 when the process is running.
- **Readiness:** `GET /ready` → 200 when the service can accept ingest and use output; 503 otherwise.
- **Metrics:** `GET /metrics` (Prometheus) when `observability.metrics_enabled = true`.

Health and metrics use `server.management_listen_address` (e.g. `:9080`) when set.

## Configuration summary

| Area         | Key options |
|-------------|-------------|
| **Server**  | `listen_address`, `tls`, `cert_file`, `key_file`, `management_listen_address` |
| **Auth**    | `token_file` or env `LOOM_SENSOR_<id>=<token>` (one token per sensor) |
| **Limits**  | `max_body_size_bytes`, `max_events_per_batch`, `max_event_size_bytes`, `per_sensor_rps` |
| **Enrichment** | `geoip_db_path`, `asn_db_path`, `dns.*` |
| **Output**  | `type`: `stdout`, `clickhouse`, or `elasticsearch`; for ClickHouse set `clickhouse_url`, optional database/table and `LOOM_CLICKHOUSE_USER` / `LOOM_CLICKHOUSE_PASSWORD`; for ES set URL/index and env credentials |
| **Logging** | `level`, `format` (json or console) |

## Deployment

- Run as a non-root user with minimal privileges.
- Store TLS certs and tokens in a secrets manager or restricted files; do not log tokens or full request/response bodies.
- For horizontal scaling, run multiple Loom instances behind a load balancer; ingest is stateless (caches such as DNS are per-process).

## Production checklist

Before going live:

| Item | Action |
|------|--------|
| **TLS** | Set `server.tls = true` and valid `cert_file` / `key_file`; config validation will fail at startup if files are missing or unreadable. |
| **Secrets** | Provide tokens via environment (`LOOM_SENSOR_*`) or a restricted `auth.token_file`; never in config or CLI. |
| **Limits** | Keep `max_body_size_bytes`, `max_events_per_batch`, and `per_sensor_rps` within spec; tune for your load. |
| **Health** | Expose `management_listen_address` and use `/health` and `/ready` for orchestration and load balancers. |
| **Metrics** | Enable `observability.metrics_enabled` and scrape `/metrics` (Prometheus); no sensitive data in labels. |
| **Logging** | Use `format = "json"` and a log level of `info` or `warn`; ensure logs (and rotation) do not capture request bodies or tokens. |
| **Output** | For ClickHouse/Elasticsearch, use TLS in the URL where possible and credentials from env. |

**Resources:** A single instance typically needs modest CPU and memory; allow enough memory for MaxMind DBs and DNS cache if enrichment is enabled. See [docs/SETUP_GUIDE.md](docs/SETUP_GUIDE.md) for full deployment and troubleshooting.

## License

See repository license.
