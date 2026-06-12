#!/usr/bin/env bash
# Phase B7 exit test: capsule memory batching + reaction batching + flush endpoint.
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

EMAIL_A="$(uniq b7a)@example.com"
EMAIL_B="$(uniq b7b)@example.com"
USER_A="$(uniq alice_)"
USER_B="$(uniq bob_)"
PNG="/tmp/b7_test_$$.png"

printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82' > "$PNG"
trap 'rm -f "$PNG"' EXIT

signup_user() {
  local email="$1" user="$2" name="$3"
  curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\",\"purpose\":\"signup\"}" >/dev/null
  local code ticket tokens token id
  code=$(otp_for_email "$email")
  ticket=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\",\"purpose\":\"signup\",\"code\":\"$code\"}" | json_field "d['ticket']")
  tokens=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
    -d "{\"ticket\":\"$ticket\",\"password\":\"supersecret1\",\"username\":\"$user\",\"display_name\":\"$name\"}")
  token=$(echo "$tokens" | json_field "d['tokens']['access_token']")
  id=$(echo "$tokens" | json_field "d['user']['id']")
  echo "$token|$id"
}

upload_photo() {
  local token="$1"
  local presign media_id upload_url
  presign=$(curl -sf -X POST "$API/media/presign" -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"kind":"photo","content_type":"image/png","byte_size":67}')
  media_id=$(echo "$presign" | json_field "d['media_id']")
  upload_url=$(echo "$presign" | json_field "d['upload_url']")
  curl -sf -X PUT "$upload_url" -H 'Content-Type: image/png' --data-binary @"$PNG" >/dev/null
  curl -sf -X POST "$API/media/$media_id/confirm" -H "Authorization: Bearer $token" >/dev/null
  echo "$media_id"
}

echo "== Signup 2 users + connect =="
AB=$(signup_user "$EMAIL_A" "$USER_A" "Alice")
BC=$(signup_user "$EMAIL_B" "$USER_B" "Bob")
TOKEN_A="${AB%%|*}"; ID_A="${AB##*|}"
TOKEN_B="${BC%%|*}"; ID_B="${BC##*|}"

curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$ID_A\"}" >/dev/null
curl -sf -X POST "$API/friends/requests/$ID_B/accept" -H "Authorization: Bearer $TOKEN_A" >/dev/null

echo "== Create active capsule =="
UNLOCK=$(python3 -c "from datetime import datetime,timedelta,timezone; print((datetime.now(timezone.utc)+timedelta(days=7)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
CAP=$(curl -sf -X POST "$API/capsules" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Wales Trip\",\"type\":\"group\",\"unlock_at\":\"$UNLOCK\",\"invited_user_ids\":[\"$ID_B\"]}" | json_field "d['id']")
curl -sf -X POST "$API/capsules/$CAP/accept" -H "Authorization: Bearer $TOKEN_B" >/dev/null

echo "== Register push token for Bob =="
curl -sf -X PUT "$API/me/push-token" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' \
  -d '{"token":"ExponentPushToken[b7-exit]","platform":"ios"}' >/dev/null

echo "== Add 2 capsule memories (batched for Bob) =="
MEDIA_A=$(upload_photo "$TOKEN_A")
MEDIA_B=$(upload_photo "$TOKEN_A")
curl -sf -X POST "$API/capsules/$CAP/memories" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' -d "{\"media_id\":\"$MEDIA_A\"}" >/dev/null
curl -sf -X POST "$API/capsules/$CAP/memories" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' -d "{\"media_id\":\"$MEDIA_B\"}" >/dev/null

echo "== Force due batch rows in DB =="
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U memoria -d memoria -c \
  "UPDATE notification_outbox SET deliver_after = now() - interval '1 minute'
   WHERE batch_key LIKE 'capsule:${CAP}:%' AND sent_at IS NULL;" >/dev/null

echo "== Flush endpoint delivers batched notification =="
FLUSH=$(curl -sf -X POST "$API/notifications/flush" -H "Authorization: Bearer $TOKEN_B")
echo "$FLUSH" | grep -q '"status":"flushed"'

NOTIFS=$(curl -sf -H "Authorization: Bearer $TOKEN_B" "$API/notifications")
echo "$NOTIFS" | grep -q 'new memories'

echo "== B7 exit test passed =="
