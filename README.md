# Loom

Loom is an enrichment service that receives batched ECS log events from [Spip](https://github.com/StefanGrimminck/Spip-Go) honeypot sensors over HTTPS, enriches each event with ASN, GEO, and optional DNS using local or cached data, and outputs enriched ECS events to a configurable destination (stdout, Elasticsearch, or similar).

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
| **Output**  | `type`: `stdout` or `elasticsearch`; for ES, set URL/index and `LOOM_ELASTICSEARCH_USER` / `LOOM_ELASTICSEARCH_PASS` in env |
| **Logging** | `level`, `format` (json or console) |

## Deployment

- Run as a non-root user with minimal privileges.
- Store TLS certs and tokens in a secrets manager or restricted files; do not log tokens or full request/response bodies.
- For horizontal scaling, run multiple Loom instances behind a load balancer; ingest is stateless (caches such as DNS are per-process).

## License

See repository license.
