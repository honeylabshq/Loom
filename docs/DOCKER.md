# Running Loom with Docker

Loom ships with a multi-stage Dockerfile that produces a minimal, non-root image suitable for production. Config, TLS certs, and secrets are **never** baked into the image; they are mounted or passed at runtime.

## Build

```bash
docker build -t loom:latest .
```

## Run

Everything Loom needs at runtime is supplied from outside the image:

| What        | How |
|------------|-----|
| Config     | Mount `loom.toml` (copy from `loom.example.toml` and edit). |
| Tokens     | Environment: `LOOM_SENSOR_<sensor_id>=<token>` or mount a token file. |
| TLS certs  | Mount a directory that contains `tls.crt` and `tls.key` where `loom.toml` points (e.g. `/etc/loom/`). |
| GeoIP/ASN  | Optional: mount `.mmdb` files at the paths set in `loom.toml` (e.g. `/var/lib/loom/`). |

### Without TLS (e.g. local dev)

```bash
cp loom.example.toml loom.toml
# Edit: set server.tls = false, leave cert_file/key_file empty

docker run -d --name loom \
  -p 8443:8443 -p 9080:9080 \
  -v "$(pwd)/loom.toml:/etc/loom/loom.toml:ro" \
  -e LOOM_SENSOR_spip01="your-secret-token" \
  loom:latest
```

### With TLS (production)

Your `loom.toml` should have something like:

```toml
[server]
tls = true
cert_file = "/etc/loom/tls.crt"
key_file = "/etc/loom/tls.key"
```

Put `loom.toml`, `tls.crt`, and `tls.key` in one directory on the host (e.g. `config/`) and mount that directory into the container at `/etc/loom`:

```bash
mkdir -p config
cp loom.example.toml config/loom.toml
# Put your cert and key in config/ as config/tls.crt and config/tls.key
# Edit config/loom.toml: cert_file = "/etc/loom/tls.crt", key_file = "/etc/loom/tls.key"

docker run -d --name loom \
  -p 8443:8443 -p 9080:9080 \
  -v "$(pwd)/config:/etc/loom:ro" \
  -e LOOM_SENSOR_spip01="your-secret-token" \
  loom:latest
```

Override config path if needed:

```bash
docker run ... loom:latest -config /etc/loom/loom.toml
```

## Docker Compose

The repo includes a `docker-compose.yml` that mounts config and uses `.env` for tokens.

**Without TLS:**  
Copy `loom.example.toml` to `loom.toml`, set `server.tls = false`, create `.env` with `LOOM_SENSOR_<id>=<token>`, then:

```bash
docker compose up -d
```

**With TLS:**  
Use a config directory so the container can see both config and certs:

1. `mkdir -p config`
2. Copy `loom.example.toml` to `config/loom.toml` and put `tls.crt` and `tls.key` in `config/`.
3. In `config/loom.toml` set `cert_file = "/etc/loom/tls.crt"` and `key_file = "/etc/loom/tls.key"`.
4. In `docker-compose.yml`, switch the volume to the config dir:
   - Comment out: `- ./loom.toml:/etc/loom/loom.toml:ro`
   - Uncomment: `- ./config:/etc/loom:ro`
5. Create `.env` with your tokens. Run: `docker compose up -d`.

**Optional (GeoIP/ASN):**  
Place the `.mmdb` files in a directory (e.g. `data/`) and add the corresponding volume lines in `docker-compose.yml` so the paths in `loom.toml` (e.g. `/var/lib/loom/GeoLite2-City.mmdb`) match.

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
