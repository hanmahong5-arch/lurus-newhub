# Single-node Deploy (32c / 32GB)

Docker Compose stack for a single host. Brings up `lurus-api` + `postgres:15`
(tuned) + `redis:7-alpine`, with resource caps that fit a 32-core / 32GB box.
Auth is delegated to an existing OIDC provider (e.g. `identity.lurus.cn`); no
HTTPS terminator — service listens on `IP:3000`.

## What you need

- A 32c / 32g Linux box with Docker ≥ 24 and Docker Compose v2
- The `lurus-hub` repo checked out
- The sibling `lurus-proto-go` repo at `../shared/lurus-proto-go` (matches the
  `go.mod` replace directive)
- Network reachability to your OIDC issuer

## 1. Configure

```bash
cd deploy/single-node
cp .env.example .env

# generate session secret
openssl rand -base64 32
# → paste into SESSION_SECRET

# pick a strong DB password
# → fill POSTGRES_PASSWORD
```

Then fill in the OIDC block. These values come from your OIDC provider console:
`OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_DEFAULT_ORG_ID`.

**Important**: register `http://<server-ip>:3000/api/v2/oauth/callback` as a
Redirect URI on that OIDC client, otherwise login will fail with
`redirect_uri_mismatch`. Update `OIDC_REDIRECT_URI` and
`OIDC_POST_LOGOUT_REDIRECT_URI` to use your real server IP.

## 2. Build the image

```bash
./build.sh
```

This stages `../shared/lurus-proto-go` into the build context, runs
`docker build`, and tags the result as `lurus-hub:local`. First run takes
5–10 minutes (bun install + go build + Vite bundle).

## 3. Bring up the stack

```bash
docker compose up -d
docker compose ps
```

Wait for `lurus-api` to report `healthy` (≈30s after `postgres` and `redis`
become healthy):

```bash
docker compose ps --format "table {{.Service}}\t{{.Status}}"
```

## 4. Verify

```bash
# API status
curl -s http://localhost:3000/api/status | jq .

# Web UI
xdg-open http://<server-ip>:3000 || echo "Open http://<server-ip>:3000 in a browser"
```

The first time you hit `/`, you'll be redirected to your OIDC provider for login. After
the OAuth round-trip you land back on `/console`.

## 5. Operate

```bash
# tail logs
docker compose logs -f lurus-api

# restart api after config change
docker compose restart lurus-api

# rebuild + redeploy after pulling new code
git pull
./build.sh && docker compose up -d

# stop everything (volumes preserved)
docker compose down

# stop + wipe DB & redis (DESTRUCTIVE)
docker compose down -v
```

Logs land in the `lurus_logs` volume. Inspect with:

```bash
docker compose exec lurus-api tail -f /app/logs/lurus-api.log
```

## Resource layout

| Service       | CPU limit | Memory limit | Notes                                |
|---------------|-----------|--------------|--------------------------------------|
| `lurus-api`   | 8         | 8 GB         | API + relay; bottleneck is I/O       |
| `postgres`    | 6         | 12 GB        | Tuned: shared_buffers=4GB, etc.      |
| `redis`       | 2         | 2 GB         | maxmemory=1.5GB, allkeys-lru         |
| **Reserved**  | 16        | 10 GB        | Headroom for kernel + container OH   |

PG knobs are baked into `command:` in `docker-compose.yml`. Edit there if you
need different sizing.

## Troubleshooting

**`COPY failed: lurus-proto-go: not found`**
You ran `docker build` directly instead of `./build.sh`. Use the script — it
stages the proto repo into the build context.

**`go: ... proxy.golang.org ... i/o timeout`** (typical on servers in China)
The default Go module proxy is unreachable. Edit `Dockerfile` and add right
after `ENV GOEXPERIMENT=greenteagc`:

```dockerfile
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
```

then re-run `./build.sh`. The two env lines are local-only — don't commit
them upstream because CI has unblocked access.

**`apt-get update` very slow (10+ minutes)**
`deb.debian.org` is throttled from your region. Lasts only on first build
(layer is cached afterwards). If you can't wait, switch to a mirror:

```dockerfile
RUN sed -i 's|deb.debian.org|mirrors.tuna.tsinghua.edu.cn|g' /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get install -y --no-install-recommends ...
```

**`pq: password authentication failed`**
You changed `POSTGRES_PASSWORD` in `.env` after the first boot. The PG volume
already has the user with the old password. Either:
- Reset: `docker compose down -v && docker compose up -d` (wipes data), or
- Fix in place: `docker compose exec postgres psql -U lurus -c "ALTER USER lurus WITH PASSWORD 'NEW';"`

**Login bounces back to login page (no error)**
The `OIDC_REDIRECT_URI` in `.env` doesn't match what's registered in the
OIDC client. Add the exact URL on the OIDC provider side. The browser console
usually shows `redirect_uri_mismatch`.

**`/api/status` returns 500 with `dial tcp: redis`**
Redis hadn't finished warming up when api started. Compose's `depends_on:
condition: service_healthy` should prevent this; if it persists, increase
Redis healthcheck `start_period`.

**Container restarts in a loop**
```bash
docker compose logs --tail=200 lurus-api
```
First boot needs to run all migrations — give it 60s before assuming it's
broken.

## Going further

- **HTTPS / domain**: drop a Caddy or Nginx in front (a Caddy example lives
  in `../Caddyfile`). Update `OIDC_REDIRECT_URI` to the public URL.
- **External Postgres**: comment out the `postgres` service and point
  `SQL_DSN` at your existing instance.
- **Backups**: `docker run --rm -v lurus-hub_pg_data:/data alpine tar czf -
  /data > pg-backup-$(date +%F).tar.gz`
