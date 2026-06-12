#!/usr/bin/env bash
# Phase B11 exit test: security smoke (rate limit, openapi, health).
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
API="$BASE/v1"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "== healthz / readyz =="
curl -sf "$BASE/healthz" | grep -q ok
curl -sf "$BASE/readyz" | grep -q ready

echo "== openapi.yaml exists =="
test -f "$ROOT/openapi.yaml"

echo "== auth endpoint rate limit returns 429 =="
# Username check: 30/min per IP (active in all envs). Global 100/min IP is production-only.
HIT=0
for i in $(seq 1 35); do
  CODE=$(curl -s -o /dev/null -w '%{http_code}' "$API/auth/username-available?u=ratelimit$i")
  if [[ "$CODE" == "429" ]]; then
    HIT=1
    break
  fi
done
if [[ "$HIT" != "1" ]]; then
  echo "expected HTTP 429 after exceeding username-available IP limit" >&2
  exit 1
fi

echo "B11 exit test passed."
