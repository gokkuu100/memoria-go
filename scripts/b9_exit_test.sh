#!/usr/bin/env bash
# Phase B9 exit test: stats heatmap, ZIP export, account deletion cascade.
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
API="$BASE/v1"
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

EMAIL="$(uniq b9)@example.com"
USER="$(uniq b9user_)"
EMAIL_B="$(uniq b9b)@example.com"
USER_B="$(uniq b9buser_)"

echo "== readyz =="
curl -sf "$BASE/readyz" | grep -q ready

echo "== Signup user A =="
curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"purpose\":\"signup\"}" >/dev/null
CODE=$(otp_for_email "$EMAIL")
TICKET=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"purpose\":\"signup\",\"code\":\"$CODE\"}" | json_field "d['ticket']")
TOKENS=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
  -d "{\"ticket\":\"$TICKET\",\"password\":\"supersecret1\",\"username\":\"$USER\",\"display_name\":\"B9 User\"}")
TOKEN=$(echo "$TOKENS" | json_field "d['tokens']['access_token']")
USER_ID=$(echo "$TOKENS" | json_field "d['user']['id']")

echo "== Stats lifetime =="
curl -sf -H "Authorization: Bearer $TOKEN" "$API/me/stats/lifetime" | grep -q memories

echo "== Stats heatmap (spark clamps from) =="
HEAT=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/me/stats/heatmap?from=2020-01-01")
FROM=$(echo "$HEAT" | json_field "d['from']")
[[ "$FROM" != "2020-01-01" ]]

echo "== Signup user B + friend =="
curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_B\",\"purpose\":\"signup\"}" >/dev/null
CODE_B=$(otp_for_email "$EMAIL_B")
TICKET_B=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_B\",\"purpose\":\"signup\",\"code\":\"$CODE_B\"}" | json_field "d['ticket']")
TOKENS_B=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
  -d "{\"ticket\":\"$TICKET_B\",\"password\":\"supersecret1\",\"username\":\"$USER_B\",\"display_name\":\"B9 Friend\"}")
TOKEN_B=$(echo "$TOKENS_B" | json_field "d['tokens']['access_token']")
USER_B_ID=$(echo "$TOKENS_B" | json_field "d['user']['id']")

curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$USER_ID\"}" >/dev/null
curl -sf -X POST "$API/friends/requests/$USER_B_ID/accept" -H "Authorization: Bearer $TOKEN" >/dev/null

echo "== Album + memory for export =="
ALBUM=$(curl -sf -X POST "$API/albums" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"B9 Export\",\"cover_style\":\"espresso\",\"invited_user_id\":\"$USER_B_ID\"}")
ALBUM_ID=$(echo "$ALBUM" | json_field "d['id']")
curl -sf -X POST "$API/albums/$ALBUM_ID/accept" -H "Authorization: Bearer $TOKEN_B" >/dev/null

PRESIGN=$(curl -sf -X POST "$API/media/presign" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"kind":"photo","content_type":"image/png","byte_size":70}')
MEDIA_ID=$(echo "$PRESIGN" | json_field "d['media_id']")
UPLOAD_URL=$(echo "$PRESIGN" | json_field "d['upload_url']")
echo "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==" | base64 -d \
  | curl -sf -X PUT "$UPLOAD_URL" -H 'Content-Type: image/png' --data-binary @- >/dev/null
curl -sf -X POST "$API/media/$MEDIA_ID/confirm" -H "Authorization: Bearer $TOKEN" >/dev/null
curl -sf -X POST "$API/albums/$ALBUM_ID/memories" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"media_id\":\"$MEDIA_ID\"}" >/dev/null

echo "== ZIP export =="
JOB=$(curl -sf -X POST "$API/exports" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"kind":"zip"}')
JOB_ID=$(echo "$JOB" | json_field "d['id']")

DOWNLOAD=""
for _ in $(seq 1 60); do
  STATUS=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/exports/$JOB_ID")
  ST=$(echo "$STATUS" | json_field "d['status']")
  if [[ "$ST" == "ready" ]]; then
    DOWNLOAD=$(echo "$STATUS" | json_field "d['download_url']")
    break
  fi
  sleep 0.5
done
[[ -n "$DOWNLOAD" ]]
curl -sf "$DOWNLOAD" | head -c 2 | grep -q PK

echo "== PDF gated on spark =="
HTTP=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/exports" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"kind":"pdf"}')
[[ "$HTTP" == "403" ]]

echo "== Account deletion =="
curl -sf -X DELETE "$API/me" -H "Authorization: Bearer $TOKEN" | grep -q account_deleted

echo "B9 exit test passed."
