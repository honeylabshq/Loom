# Running Loom with Docker

Loom ships with a multi-stage Dockerfile that produces a minimal, non-root image suitable for production.

## Build

```bash
docker build -t loom:latest .
```

## Run

Config and secrets are supplied at runtime (never baked into the image):

1. **Config file** — Mount your `loom.toml` (copy from `loom.example.toml` and edit).
2. **Tokens** — Set `LOOM_SENSOR_<id>=<token>` in the environment (e.g. `--env` or `env_file`).
3. **TLS (production)** — Mount cert and key where `loom.toml` points (e.g. `/etc/loom/`).
4. **GeoIP/ASN (optional)** — Mount MaxMind `.mmdb` files if you use enrichment.

Example with bind mounts:

```bash
# Ensure config is readable by UID 1000 (container runs as non-root)
cp loom.example.toml loom.toml
# Edit loom.toml, then:

docker run -d --name loom \
  -p 8443:8443 -p 9080:9080 \
  -v "$(pwd)/loom.toml:/etc/loom/loom.toml:ro" \
  -e LOOM_SENSOR_spip01="your-secret-token" \
  loom:latest
```

Override config path if needed:

```bash
docker run ... loom:latest -config /path/in/container/loom.toml
```

## Docker Compose

An example `docker-compose.yml` is in the repo root. Use it as a template:

1. Copy `loom.example.toml` to `loom.toml` and configure it.
2. Create a `.env` file with your tokens (e.g. `LOOM_SENSOR_spip01=secret`). Do not commit `.env`.
3. Run:

```bash
docker compose up -d
```

Health check:

```bash
curl -s http://localhost:9080/health
```

## Security

- **Non-root:** The container runs as user `loom` (UID 1000). Ensure mounted config and certs are readable by that user (e.g. `chmod 644` on the host, or bind-mount from a directory owned by UID 1000).
- **No secrets in image:** Tokens and credentials come only from environment or mounted files at runtime.
- **Read-only config:** Mount `loom.toml` (and certs) with `:ro` so the process cannot modify them.
- **Minimal image:** Based on Alpine; only the binary and ca-certificates. No shell required for normal run (add one for debugging if needed with `docker run -it ... sh` by overriding entrypoint).

## Ports

| Port | Purpose |
|------|--------|
| 8443 | Ingest API (HTTPS in production) |
| 9080 | Management (health, readiness, metrics) |

Adjust in `loom.toml` and in your port mappings if you use different values.
