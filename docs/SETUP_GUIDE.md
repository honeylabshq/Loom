# Setup Guide: One Loom + One Spip

This guide walks through deploying **one Loom instance** and **one Spip sensor** so that Spip sends connection events to Loom, and Loom enriches and outputs them.

---

## Table of contents

1. [Architecture](#1-architecture)
2. [Prerequisites](#2-prerequisites)
3. [Build both components](#3-build-both-components)
4. [Option A: Development setup (same machine, no TLS)](#4-option-a-development-setup-same-machine-no-tls)
5. [Option B: Production-style setup (TLS, optional enrichment)](#5-option-b-production-style-setup-tls-optional-enrichment)
6. [Verification and troubleshooting](#6-verification-and-troubleshooting)
7. [Reference: config cross-check](#7-reference-config-cross-check)

---

## 1. Architecture

```
  [ Internet / scanners ]
           |
           v  TCP (e.g. port 8080)
  +------------------+
  |  Spip (sensor)   |  Listens for connections, logs each as ECS JSON.
  |  - config.toml   |  Optionally batches events and POSTs to Loom.
  +--------+---------+
           |
           |  HTTPS (or HTTP in dev)  POST /ingest  (Bearer token, X-Spip-ID)
           v
  +------------------+
  |  Loom            |  Validates auth, enriches (ASN, GEO, optional DNS),
  |  - loom.toml     |  outputs one ECS doc per event (stdout, file, or Elasticsearch).
  +------------------+
           |
           v
  [ Stdout / Elasticsearch / SIEM ]
```

- **Spip** runs once per sensor. It opens a TCP listen port, captures traffic, and for each connection emits an ECS-shaped event. If Loom is enabled, it batches those events and POSTs them to the Loom URL.
- **Loom** runs once (single instance in this guide). It exposes an ingest API (and optionally a management port for health/metrics). It does not run as root; Spip may need root only if you use iptables redirects.

---

## 2. Prerequisites

- **Go** 1.21+ (Loom), 1.24+ (Spip) — for building from source.
- **Linux** — Spip’s iptables-based redirect is Linux-specific; you can still run both on another OS without iptables (bind Spip to a port and connect to it directly for testing).
- **Network** — Spip must be able to reach Loom’s ingest URL (hostname/IP and port).
- **Secrets** — Decide how you will provide the Loom **token** (env var or token file). Spip needs the same token in its config (and a unique `sensor_id`).
- **Optional for production:** TLS key and certificate for Loom; MaxMind GeoLite2 databases for ASN/GEO enrichment.

---

## 3. Build both components

### Loom

```bash
cd /path/to/Loom
go build -o loom ./cmd/loom
```

### Spip

```bash
cd /path/to/Spip-Go
go build -o spip-agent ./cmd/spip-agent
```

Keep the binaries (e.g. `./loom` and `./spip-agent`) and their config files available for the steps below.

---

## 4. Option A: Development setup (same machine, no TLS)

Use this for a quick local test: Loom and Spip on the same host, HTTP (no TLS), Loom writing enriched events to stdout.

### 4.1 Loom config

Create a Loom config (e.g. `loom.toml`) with TLS disabled and stdout output:

```toml
[server]
listen_address = ":8080"
tls = false
management_listen_address = ":9080"

[limits]
max_body_size_bytes = 2097152
max_events_per_batch = 500
max_event_size_bytes = 131072
per_sensor_rps = 100

[enrichment.dns]
enabled = false

[output]
type = "stdout"

[logging]
level = "info"
format = "console"

[observability]
metrics_enabled = true
```

You **must** provide at least one token. Use an environment variable (no token in the config file):

```bash
export LOOM_SENSOR_spip01="dev-token-please-change"
```

Use a single word or hyphenated id (e.g. `spip01`). This will be the sensor id that Spip must send as `X-Spip-ID`.

### 4.2 Spip config

Create Spip’s config (e.g. `config.toml`). Use the **same** token and a **matching** sensor id. The Loom URL must point to Loom’s ingest port (here `8080`). For same-machine dev, use `http://127.0.0.1:8080` (path is optional; Loom accepts `/`, `/ingest`, and `/api/v1/ingest`):

```toml
name = "spip-agent"
ip = "127.0.0.1"
port = 8080

[loom]
enabled = true
url = "http://127.0.0.1:8080/"
sensor_id = "spip01"
token = "dev-token-please-change"
batch_size = 10
flush_interval = "5s"
```

- `sensor_id` must match the id you used in `LOOM_SENSOR_<id>` (here `spip01`).
- `token` must match the value of `LOOM_SENSOR_spip01`.

### 4.3 Start Loom, then Spip

Terminal 1 — start Loom (with the token env set):

```bash
export LOOM_SENSOR_spip01="dev-token-please-change"
./loom -config loom.toml
```

Wait until you see that the ingest server is listening (e.g. “ingest server listening (no TLS) addr=:8080”).

Terminal 2 — start Spip:

```bash
./spip-agent -config config.toml
```

### 4.4 Generate traffic and check output

- Trigger a connection to Spip (e.g. `nc 127.0.0.1 8080` or hit the port with a browser).
- In **Terminal 1** (Loom), you should see one JSON line per event (enriched ECS). Spip’s own logs (stdout or log_file) will also show the connection.
- Optional: call Loom’s health and readiness:
  - `curl -s http://127.0.0.1:9080/health`
  - `curl -s http://127.0.0.1:9080/ready`

---

## 5. Option B: Production-style setup (TLS, optional enrichment)

This section assumes you want TLS on Loom, optional GeoIP/ASN enrichment, and either stdout or Elasticsearch as output.

### 5.1 TLS certificate for Loom

Use a proper certificate in production. For testing, you can use a self-signed cert:

```bash
mkdir -p /etc/loom
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout /etc/loom/tls.key \
  -out /etc/loom/tls.crt \
  -subj "/CN=loom.local"
chmod 600 /etc/loom/tls.key
```

Adjust paths and `CN` to match your environment.

### 5.2 Loom config (production-style)

Example `loom.toml` with TLS, management port, and optional GeoIP/ASN:

```toml
[server]
listen_address = ":8443"
tls = true
cert_file = "/etc/loom/tls.crt"
key_file = "/etc/loom/tls.key"
management_listen_address = ":9080"

[limits]
max_body_size_bytes = 2097152
max_events_per_batch = 500
max_event_size_bytes = 131072
per_sensor_rps = 50

[enrichment]
# Optional: download GeoLite2-City.mmdb and GeoLite2-ASN.mmdb from MaxMind
# geoip_db_path = "/var/lib/loom/GeoLite2-City.mmdb"
# asn_db_path = "/var/lib/loom/GeoLite2-ASN.mmdb"

[enrichment.dns]
enabled = false

[output]
type = "stdout"
# Or Elasticsearch:
# type = "elasticsearch"
# elasticsearch_url = "https://localhost:9200"
# elasticsearch_index = "loom-events"
# (set LOOM_ELASTICSEARCH_USER / LOOM_ELASTICSEARCH_PASS in env if needed)

[logging]
level = "info"
format = "json"

[observability]
metrics_enabled = true
```

Provide the token via environment (recommended) or token file:

```bash
export LOOM_SENSOR_spip01="your-secure-token"
```

If you use a token file instead, create e.g. `/etc/loom/tokens.txt` with one line per sensor: `token,sensor_id`, and in `loom.toml`:

```toml
[auth]
token_file = "/etc/loom/tokens.txt"
```

### 5.3 Spip config (pointing at Loom over HTTPS)

Spip must use the **full Loom ingest URL** (scheme, host, port, and path if you use one). For a self-signed Loom cert, set `insecure_skip_verify = true`:

```toml
name = "spip-agent"
ip = "0.0.0.0"
port = 8080

[loom]
enabled = true
url = "https://LOOM_HOST:8443/ingest"
sensor_id = "spip01"
token = "your-secure-token"
batch_size = 50
flush_interval = "10s"
insecure_skip_verify = true
```

Replace `LOOM_HOST` with the hostname or IP that Spip uses to reach Loom (e.g. `127.0.0.1` for same host, or the server’s IP). Use `https` and port `8443` (or whatever `listen_address` uses). Path can be `/`, `/ingest`, or `/api/v1/ingest`.

### 5.4 Optional: MaxMind GeoLite2 for ASN/GEO

1. Sign up at [MaxMind GeoLite2](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data) and download:
   - GeoLite2-City.mmdb  
   - GeoLite2-ASN.mmdb  
2. Place them where Loom can read them (e.g. `/var/lib/loom/`).  
3. In Loom’s `[enrichment]`, set `geoip_db_path` and `asn_db_path`.  
4. Restart Loom. Enriched events will include `source.geo.*` and `source.as.*` when the DBs contain data for the source IP.

### 5.5 Start order and firewall

1. Start **Loom** first (with token env or token file in place).  
2. Start **Spip**.  
3. Ensure the host/port for Loom’s ingest (e.g. 8443) is reachable from the host where Spip runs (firewall / security groups).

---

## 6. Verification and troubleshooting

### 6.1 Health and readiness

- **Liveness:** `GET /health` or `GET /live` on the **management** port (e.g. 9080).  
  - Example: `curl -s http://127.0.0.1:9080/health`  
  - Expect: body `ok` and HTTP 200.
- **Readiness:** `GET /ready` on the same port.  
  - Expect: 200 when Loom can accept ingest and use output; 503 if not ready.

### 6.2 Test ingest with curl

You can simulate a minimal batch without Spip (replace URL and token/sensor as needed):

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  -X POST "http://127.0.0.1:8080/ingest" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer dev-token-please-change" \
  -H "X-Spip-ID: spip01" \
  -d '[{"@timestamp":"2025-02-15T12:00:00Z","event":{"id":"test-1","ingested_by":"spip"},"source":{"ip":"8.8.8.8","port":12345},"destination":{"ip":"127.0.0.1","port":8080}}]'
```

- **204** (or 200): success.  
- **401**: wrong or missing token, or `X-Spip-ID` does not match the token’s sensor.  
- **413**: body or batch too large.  
- **429**: per-sensor rate limit exceeded.

### 6.3 Spip stderr

If Loom is unreachable or returns an error, Spip logs to stderr (e.g. “loom: …”). Check:

- `loom POST: …` — connection or TLS problem.  
- `loom POST: status 401` — token or sensor id mismatch.  
- `loom POST: status 4xx/5xx` — see Loom logs and the response body (Loom does not log tokens or full bodies).

### 6.4 Common issues

| Symptom | What to check |
|--------|----------------|
| Loom: “auth: no tokens configured” | Set `LOOM_SENSOR_<sensor_id>=<token>` (or use `auth.token_file`) before starting Loom. |
| Spip: 401 from Loom | Token in Spip’s `[loom]` must equal the token Loom has for that sensor. `X-Spip-ID` sent by Spip must match the sensor id tied to that token (e.g. `spip01`). |
| Spip: connection refused / timeout | Loom not running; wrong host/port in `loom.url`; firewall blocking Loom’s ingest port. |
| Loom: “server: tls enabled but cert_file or key_file missing” | Either set `tls = false` (dev) or set both `cert_file` and `key_file` to valid paths. |
| No enriched fields (ASN/GEO) | Enrichment is optional. To get them, configure `enrichment.geoip_db_path` and `enrichment.asn_db_path` and ensure the DBs are present and readable. |

---

## 7. Reference: config cross-check

Use this to ensure Loom and Spip agree.

| Item | Loom | Spip |
|------|------|------|
| **Token** | From env `LOOM_SENSOR_<id>=<token>` or `auth.token_file` | `[loom]` section: `token = "…"` — must match. |
| **Sensor id** | In env key or token file as the id for that token | `[loom]` section: `sensor_id = "…"` — must match. |
| **URL** | Ingest listen: `server.listen_address` (e.g. `:8080` or `:8443`) | `[loom]` section: `url = "http(s)://HOST:PORT/"` or `…/ingest` — must reach Loom. |
| **TLS** | `server.tls`, `cert_file`, `key_file` | For HTTPS URL, use `insecure_skip_verify = true` only if using a self-signed cert. |

Single-sensor checklist:

1. Choose a **sensor id** (e.g. `spip01`).  
2. Choose a **token** (e.g. a long random string).  
3. **Loom:** `export LOOM_SENSOR_spip01="<token>"` (or add that line to the token file).  
4. **Spip:** In `[loom]`, set `sensor_id = "spip01"` and `token = "<token>"`.  
5. **Spip:** Set `url` to Loom’s ingest base URL (including port and, if you like, path like `/ingest`).

After that, start Loom, then Spip, and verify with health checks and a test connection to Spip.
