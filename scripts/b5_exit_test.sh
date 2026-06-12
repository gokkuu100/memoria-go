#!/usr/bin/env bash
# Phase B5 exit test: two users run create → accept → contribute → react → comment → leave.
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

EMAIL_A="$(uniq b5a)@example.com"
EMAIL_B="$(uniq b5b)@example.com"
USER_A="$(uniq alice_)"
USER_B="$(uniq bob_)"
PNG="/tmp/b5_test_$$.png"

# 1x1 PNG
printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82' > "$PNG"
trap 'rm -f "$PNG"' EXIT

echo "== OTP + signup user A =="
curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_A\",\"purpose\":\"signup\"}" >/dev/null
CODE_A=$(otp_for_email "$EMAIL_A")
TICKET_A=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_A\",\"purpose\":\"signup\",\"code\":\"$CODE_A\"}" | json_field "d['ticket']")
TOKENS_A=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
  -d "{\"ticket\":\"$TICKET_A\",\"password\":\"supersecret1\",\"username\":\"$USER_A\",\"display_name\":\"Alice\"}")
TOKEN_A=$(echo "$TOKENS_A" | json_field "d['tokens']['access_token']")
ID_A=$(echo "$TOKENS_A" | json_field "d['user']['id']")

echo "== OTP + signup user B =="
curl -sf -X POST "$API/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_B\",\"purpose\":\"signup\"}" >/dev/null
CODE_B=$(otp_for_email "$EMAIL_B")
TICKET_B=$(curl -sf -X POST "$API/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL_B\",\"purpose\":\"signup\",\"code\":\"$CODE_B\"}" | json_field "d['ticket']")
TOKENS_B=$(curl -sf -X POST "$API/auth/signup" -H 'Content-Type: application/json' \
  -d "{\"ticket\":\"$TICKET_B\",\"password\":\"supersecret1\",\"username\":\"$USER_B\",\"display_name\":\"Bob\"}")
TOKEN_B=$(echo "$TOKENS_B" | json_field "d['tokens']['access_token']")
ID_B=$(echo "$TOKENS_B" | json_field "d['user']['id']")

echo "== Connect friends =="
curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$ID_A\"}" >/dev/null
curl -sf -X POST "$API/friends/requests/$ID_B/accept" -H "Authorization: Bearer $TOKEN_A" >/dev/null

echo "== Create album + accept =="
ALBUM=$(curl -sf -X POST "$API/albums" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Weekend\",\"cover_style\":\"espresso\",\"invited_user_id\":\"$ID_B\"}" | json_field "d['id']")
curl -sf -X POST "$API/albums/$ALBUM/accept" -H "Authorization: Bearer $TOKEN_B" >/dev/null

echo "== Upload photo + add memory =="
PRESIGN=$(curl -sf -X POST "$API/media/presign" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' \
  -d '{"kind":"photo","content_type":"image/png","byte_size":67}')
MEDIA_ID=$(echo "$PRESIGN" | json_field "d['media_id']")
UPLOAD_URL=$(echo "$PRESIGN" | json_field "d['upload_url']")
curl -sf -X PUT "$UPLOAD_URL" -H 'Content-Type: image/png' --data-binary @"$PNG" >/dev/null
curl -sf -X POST "$API/media/$MEDIA_ID/confirm" -H "Authorization: Bearer $TOKEN_A" >/dev/null
MEMORY=$(curl -sf -X POST "$API/albums/$ALBUM/memories" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' -d "{\"media_id\":\"$MEDIA_ID\",\"caption\":\"hi\"}" | json_field "d['id']")

echo "== React + comment =="
curl -sf -X PUT "$API/memories/$MEMORY/reactions" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d '{"emoji":"❤️"}' >/dev/null
curl -sf -X POST "$API/memories/$MEMORY/comments" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d '{"body":"nice!"}' >/dev/null

echo "== List memories =="
COUNT=$(curl -sf -H "Authorization: Bearer $TOKEN_B" "$API/albums/$ALBUM/memories" | json_field "len(d['memories'])")
[[ "$COUNT" == "1" ]]

echo "== Alice leaves (deletes her photo) =="
curl -sf -X POST "$API/albums/$ALBUM/leave" -H "Authorization: Bearer $TOKEN_A" >/dev/null
COUNT=$(curl -sf -H "Authorization: Bearer $TOKEN_B" "$API/albums/$ALBUM/memories" | json_field "len(d['memories'])")
[[ "$COUNT" == "0" ]]

echo "PASS: B5 exit test — full album lifecycle"
