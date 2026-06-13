# Memoria API

Go backend for Memoria. Architecture: `../docs/SYSTEM_DESIGN.md`.
Phased plan + feature checklists: `../docs/BACKEND_PLAN.md`.

## Stack

Go · chi · PostgreSQL (pgx + sqlc) · goose migrations · Cloudflare R2 (MinIO locally) · Docker

## Quick start

```bash
cp .env.example .env
make tools        # installs goose, sqlc, golangci-lint (needs Go on PATH)
make up           # api + postgres + minio via docker compose
curl localhost:8080/readyz
```

Local iteration without rebuilding the image:

```bash
docker compose up -d postgres minio
make run          # runs the API on the host against compose postgres
```

## Common tasks

| Command | What |
|---|---|
| `make migrate-new name=add_users` | New migration in `db/migrations` |
| `make migrate-up` / `migrate-down` | Apply / roll back migrations |
| `make sqlc` | Regenerate `internal/dbgen` from `db/queries` |
| `make test` / `make lint` | Tests / lint |
| `make openapi-validate` | Parse-check `openapi.yaml` |

Migrations also run automatically on API startup (embedded via `db/db.go`).

## Environment

All config is env vars — see `.env.example` for the full annotated list.
Development has working defaults for everything except `DATABASE_URL`;
production additionally requires `JWT_SECRET`, `SMTP_HOST`, the S3 set
(`S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`), and
`REVENUECAT_WEBHOOK_SECRET`. Optional: `SENTRY_DSN`, `DB_MAX_CONNS` (default 20).

Ops runbooks: `../docs/RUNBOOK.md` (backups, load test, deploy). Security checklist:
`../docs/SECURITY.md`.
`S3_PUBLIC_ENDPOINT` is the host clients see in presigned URLs (defaults to
`S3_ENDPOINT`; inside compose the API uses `http://minio:9000` while clients
need `http://localhost:9000`).

**Gmail SMTP:** use an [App Password](https://myaccount.google.com/apppasswords).
`SMTP_FROM` must match the authenticated Gmail account unless you configure a
Google Workspace **Send mail as** alias (e.g. `noreply@memoria.com`).

## Layout

```
cmd/api/          entrypoint
internal/
  config/         env config
  server/         router + middleware
  httpx/          JSON + error envelope helpers
  billing/        plan limits + RevenueCat webhook + GET /me/subscription
  media/          presign/confirm/download + S3 store + orphan sweep
  jobs/           cron runner (pg advisory-locked jobs)
  dbgen/          sqlc-generated (do not edit)
  sentryx/        optional Sentry wiring
openapi.yaml      hand-maintained API spec (all /v1 endpoints)
scripts/
  backup.sh       pg_dump helper (upload to R2 — see RUNBOOK)
  b11_exit_test.sh
  load/k6_album_presign.js
db/
  migrations/     goose SQL migrations (never edit applied ones)
  queries/        sqlc query sources
```
