#!/usr/bin/env bash
# Phase B10 exit test: widget feed + photo thumbnail generation.
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

EMAIL="$(uniq b10)@example.com"
USER="$(uniq b10user_)"
EMAIL_B="$(uniq b10b)@example.com"
USER_B="$(uniq b10buser_)"

echo "== readyz =="
curl -sf "$BASE/readyz" | grep -q ready

echo "== Signup user A =="
curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"purpose\":\"signup\"}" >/dev/null
CODE=$(otp_for_email "$EMAIL")
TICKET=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"purpose\":\"signup\",\"code\":\"$CODE\"}" | json_field "d['ticket']")
TOKENS=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
  -d "{\"ticket\":\"$TICKET\",\"password\":\"supersecret1\",\"username\":\"$USER\",\"display_name\":\"B10 User\"}")
TOKEN=$(echo "$TOKENS" | json_field "d['tokens']['access_token']")
USER_ID=$(echo "$TOKENS" | json_field "d['user']['id']")

echo "== Signup user B + friend =="
curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_B\",\"purpose\":\"signup\"}" >/dev/null
CODE_B=$(otp_for_email "$EMAIL_B")
TICKET_B=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_B\",\"purpose\":\"signup\",\"code\":\"$CODE_B\"}" | json_field "d['ticket']")
TOKENS_B=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
  -d "{\"ticket\":\"$TICKET_B\",\"password\":\"supersecret1\",\"username\":\"$USER_B\",\"display_name\":\"B10 Friend\"}")
TOKEN_B=$(echo "$TOKENS_B" | json_field "d['tokens']['access_token']")
USER_B_ID=$(echo "$TOKENS_B" | json_field "d['user']['id']")

curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$USER_ID\"}" >/dev/null
curl -sf -X POST "$API/friends/requests/$USER_B_ID/accept" -H "Authorization: Bearer $TOKEN" >/dev/null

echo "== Album + photo memory =="
ALBUM=$(curl -sf -X POST "$API/albums" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"B10 Widget\",\"cover_style\":\"espresso\",\"invited_user_id\":\"$USER_B_ID\"}")
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

echo "== Widget feed has album thumb =="
FEED=$(curl -sf -H "Authorization: Bearer $TOKEN_B" "$API/widget/feed")
echo "$FEED" | grep -q 'album_photo'
echo "$FEED" | grep -q 'thumb_url'

echo "== Sealed capsule countdown (no media URL) =="
UNLOCK_AT=$(date -u -d '+2 days' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+2d +%Y-%m-%dT%H:%M:%SZ)
CAP=$(curl -sf -X POST "$API/capsules" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"B10 Seal\",\"type\":\"solo\",\"unlock_at\":\"$UNLOCK_AT\"}")
CAP_ID=$(echo "$CAP" | json_field "d['id']")

PRESIGN2=$(curl -sf -X POST "$API/media/presign" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"kind":"photo","content_type":"image/png","byte_size":70}')
MEDIA2=$(echo "$PRESIGN2" | json_field "d['media_id']")
UP2=$(echo "$PRESIGN2" | json_field "d['upload_url']")
echo "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==" | base64 -d \
  | curl -sf -X PUT "$UP2" -H 'Content-Type: image/png' --data-binary @- >/dev/null
curl -sf -X POST "$API/media/$MEDIA2/confirm" -H "Authorization: Bearer $TOKEN" >/dev/null
curl -sf -X POST "$API/capsules/$CAP_ID/memories" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d "{\"media_id\":\"$MEDIA2\"}" >/dev/null

FEED2=$(curl -sf -H "Authorization: Bearer $TOKEN" "$API/widget/feed")
echo "$FEED2" | grep -q 'capsule_countdown'
echo "$FEED2" | grep -q 'unlock_at'

echo "B10 exit test passed."
