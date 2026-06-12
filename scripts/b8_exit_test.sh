#!/usr/bin/env bash
# Phase B8 exit test: RevenueCat webhook → plan update → GET /me/subscription.
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
API="$BASE/v1"
WEBHOOK_SECRET="${REVENUECAT_WEBHOOK_SECRET:-dev-revenuecat-webhook-secret}"
PRODUCT_PRO="${REVENUECAT_PRODUCT_PRO:-memoria_pro}"
COMPOSE_FILE="$(cd "$(dirname "$0")/.." && pwd)/docker-compose.yml"

json_field() {
  python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"
}

otp_for_email() {
  local email="$1"
  sleep 0.3
  docker compose -f "$COMPOSE_FILE" logs api 2>/dev/null \
    | grep "mailer (dev log mode)" \
    | grep "$email" \
    | tail -1 \
    | grep -oE 'code is [0-9]{6}' \
    | grep -oE '[0-9]{6}'
}

uniq() { echo "${1}$(date +%s%N | tail -c 9)"; }

EMAIL="$(uniq b8)@example.com"
USER="$(uniq b8user_)"
EVENT_ID="b8_exit_$(date +%s%N)"
EXP_MS=$(python3 -c "import time; print(int((time.time()+86400*30)*1000))")

echo "== readyz =="
curl -sf "$BASE/readyz" | grep -q ready

echo "== Signup =="
curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"purpose\":\"signup\"}" >/dev/null
CODE=$(otp_for_email "$EMAIL")
TICKET=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"purpose\":\"signup\",\"code\":\"$CODE\"}" | json_field "d['ticket']")
TOKENS=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
  -d "{\"ticket\":\"$TICKET\",\"password\":\"supersecret1\",\"username\":\"$USER\",\"display_name\":\"B8 User\"}")
TOKEN=$(echo "$TOKENS" | json_field "d['tokens']['access_token']")
USER_ID=$(echo "$TOKENS" | json_field "d['user']['id']")

echo "== Spark subscription before purchase =="
SUB=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/me/subscription")
PLAN=$(echo "$SUB" | json_field "d['plan']")
[[ "$PLAN" == "spark" ]]

echo "== RevenueCat INITIAL_PURCHASE webhook (pro) =="
curl -sf -X POST "$API/webhooks/revenuecat" \
  -H "Authorization: Bearer $WEBHOOK_SECRET" \
  -H 'Content-Type: application/json' \
  -d "{
    \"api_version\": \"1.0\",
    \"event\": {
      \"id\": \"$EVENT_ID\",
      \"type\": \"INITIAL_PURCHASE\",
      \"app_user_id\": \"$USER_ID\",
      \"product_id\": \"$PRODUCT_PRO\",
      \"expiration_at_ms\": $EXP_MS
    }
  }" | grep -q '"status":"ok"'

echo "== Idempotent retry =="
curl -sf -X POST "$API/webhooks/revenuecat" \
  -H "Authorization: Bearer $WEBHOOK_SECRET" \
  -H 'Content-Type: application/json' \
  -d "{
    \"api_version\": \"1.0\",
    \"event\": {
      \"id\": \"$EVENT_ID\",
      \"type\": \"INITIAL_PURCHASE\",
      \"app_user_id\": \"$USER_ID\",
      \"product_id\": \"$PRODUCT_PRO\",
      \"expiration_at_ms\": $EXP_MS
    }
  }" | grep -q duplicate

echo "== Pro subscription after purchase =="
SUB=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/me/subscription")
PLAN=$(echo "$SUB" | json_field "d['plan']")
CAPS=$(echo "$SUB" | json_field "d['limits']['max_active_capsules']")
[[ "$PLAN" == "pro" ]]
[[ "$CAPS" == "None" || "$CAPS" == "null" ]]

echo "B8 exit test passed."
