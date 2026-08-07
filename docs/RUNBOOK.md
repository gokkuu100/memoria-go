# Memoria API — Operations Runbook (B11)

## Local / staging

```bash
cd memoria-gobackend
cp .env.example .env
make up
curl localhost:8080/readyz
make test
./scripts/b11_exit_test.sh
```

## Production deploy (requires your credentials)

1. **Build & push image** — CI builds on every push (`.github/workflows/ci.yml`).
   Tag and deploy the image to your host (Fly.io, Hetzner VPS, Railway, etc.).
2. **Environment** — set at minimum:
   - `ENV=production`
   - `DATABASE_URL` (managed Postgres, SSL)
   - `JWT_SECRET` (random 32+ bytes)
   - `SMTP_*` (Gmail app password or transactional provider)
   - `S3_*` (Cloudflare R2 endpoint + keys + bucket)
   - `S3_PUBLIC_ENDPOINT` (CDN host clients use in presigned URLs)
   - `REVENUECAT_WEBHOOK_SECRET`
   - `EXPO_ACCESS_TOKEN` (optional but recommended)
   - `SENTRY_DSN` (optional)
   - `DB_MAX_CONNS` (default `20`; raise with Postgres `max_connections`)
3. **Reverse proxy** — terminate TLS (Caddy/nginx). Forward client IP:
   - Set `X-Forwarded-For` / `X-Real-IP` from the proxy only (strip client-supplied values).
   - Rate limits use `httpx.ClientIP` which prefers `X-Forwarded-For` / `X-Real-IP`, then `RemoteAddr`.
   - Serve the media bucket on the same HTTPS host (path-style `/<bucket>/…`) so clients are not forced onto `:9000`. Do **not** enable gzip on the MinIO reverse proxy.
4. **Health checks** — liveness `GET /healthz`, readiness `GET /readyz` (DB ping).

## Database pool sizing

- `DB_MAX_CONNS` env (default **20**) → `pgxpool.MaxConns` (`internal/pg/pool.go`).
- Rule of thumb: `(num_api_instances × DB_MAX_CONNS) < postgres_max_connections − 10`.
- Cron jobs share the same pool on each API instance; advisory locks prevent duplicate work.

## Nightly backups → R2

Script: `memoria-gobackend/scripts/backup.sh`

```bash
export DATABASE_URL='postgres://...'
export BACKUP_DIR=/var/backups/memoria
export BACKUP_RETENTION_DAYS=30
./scripts/backup.sh
```

**Upload to private R2 bucket** (configure once with your R2 credentials):

```bash
# Install rclone or aws cli configured for R2
aws s3 cp backups/memoria-*.sql.gz s3://memoria-backups/db/ \
  --endpoint-url https://<accountid>.r2.cloudflarestorage.com
```

Cron example (UTC 03:00):

```cron
0 3 * * * cd /opt/memoria/memoria-gobackend && DATABASE_URL='...' ./scripts/backup.sh && aws s3 sync backups/ s3://memoria-backups/db/ --endpoint-url ...
```

### Restore test (run once before launch)

```bash
gunzip -c backups/memoria-YYYYMMDD.sql.gz | psql "$RESTORE_DATABASE_URL"
curl https://api.example.com/readyz
```

## Load test (k6)

Requires [k6](https://k6.io/) and a valid access token from a test account.

```bash
export K6_TOKEN=<access_token>
k6 run memoria-gobackend/scripts/load/k6_album_presign.js
```

### Baseline (local compose, 2026-06-12)

| Metric | Target | Local baseline |
|---|---|---|
| VUs | 500 peak | 500 ramp |
| `http_req_failed` | < 5% | Run locally to record |
| `http_req_duration` p95 | < 2s | Run locally to record |

> Re-run after production sizing and record results in this table.

## Staging environment

Not auto-provisioned in-repo. Suggested flow:

1. GitHub Actions CI green on `main`.
2. Deploy same Docker image to staging with sandbox R2 + Resend.
3. Run Phase 6 exit scenario (`scripts/b6_exit_test.sh`) against staging `BASE_URL`.
4. Run load test with staging token.
5. Promote image tag to production.

## OpenAPI / TS client

- Spec: `memoria-gobackend/openapi.yaml`
- Validate: `make openapi-validate`
- Future app types: `./scripts/generate-ts-client.sh` (stub → `memoria/src/api/generated`)

## Observability

- Logs: structured JSON (`slog`) to stdout in production.
- Errors: optional Sentry via `SENTRY_DSN`.
- Alerts: monitor `/readyz` + Sentry error rate + backup job success.
