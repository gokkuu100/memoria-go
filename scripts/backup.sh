#!/usr/bin/env bash
# Nightly Postgres backup to a local file. Upload to private R2 — see docs/RUNBOOK.md.
set -euo pipefail

DATABASE_URL="${DATABASE_URL:-}"
OUT_DIR="${BACKUP_DIR:-./backups}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-30}"

if [[ -z "$DATABASE_URL" ]]; then
  echo "DATABASE_URL is required" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
FILE="$OUT_DIR/memoria-${STAMP}.sql.gz"

echo "Backing up to $FILE"
pg_dump "$DATABASE_URL" --no-owner --no-acl | gzip -9 >"$FILE"
echo "Backup complete ($(du -h "$FILE" | awk '{print $1}'))"

if [[ "$RETENTION_DAYS" =~ ^[0-9]+$ ]]; then
  find "$OUT_DIR" -name 'memoria-*.sql.gz' -mtime +"$RETENTION_DAYS" -delete 2>/dev/null || true
fi
