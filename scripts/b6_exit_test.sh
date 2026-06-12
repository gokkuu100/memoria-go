#!/usr/bin/env bash
# Phase B6 exit test: group capsule with 3 users — invite, contribute, freeze,
# unfreeze, unlock, react.
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

EMAIL_A="$(uniq b6a)@example.com"
EMAIL_B="$(uniq b6b)@example.com"
EMAIL_C="$(uniq b6c)@example.com"
USER_A="$(uniq alice_)"
USER_B="$(uniq bob_)"
USER_C="$(uniq carol_)"
PNG="/tmp/b6_test_$$.png"

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

echo "== Signup 3 users =="
AB=$(signup_user "$EMAIL_A" "$USER_A" "Alice")
BC=$(signup_user "$EMAIL_B" "$USER_B" "Bob")
CC=$(signup_user "$EMAIL_C" "$USER_C" "Carol")
TOKEN_A="${AB%%|*}"; ID_A="${AB##*|}"
TOKEN_B="${BC%%|*}"; ID_B="${BC##*|}"
TOKEN_C="${CC%%|*}"; ID_C="${CC##*|}"

echo "== Connect friends =="
curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$ID_A\"}" >/dev/null
curl -sf -X POST "$API/friends/requests/$ID_B/accept" -H "Authorization: Bearer $TOKEN_A" >/dev/null
curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_C" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$ID_A\"}" >/dev/null
curl -sf -X POST "$API/friends/requests/$ID_C/accept" -H "Authorization: Bearer $TOKEN_A" >/dev/null

echo "== Create group capsule + accept invites =="
UNLOCK=$(python3 -c "from datetime import datetime,timedelta,timezone; print((datetime.now(timezone.utc)+timedelta(hours=24)).strftime('%Y-%m-%dT%H:%M:%SZ'))")
CAP=$(curl -sf -X POST "$API/capsules" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Wales Trip\",\"type\":\"group\",\"unlock_at\":\"$UNLOCK\",\"invited_user_ids\":[\"$ID_B\",\"$ID_C\"]}" | json_field "d['id']")
curl -sf -X POST "$API/capsules/$CAP/accept" -H "Authorization: Bearer $TOKEN_B" >/dev/null
curl -sf -X POST "$API/capsules/$CAP/accept" -H "Authorization: Bearer $TOKEN_C" >/dev/null

echo "== Sealed: memories list returns 403 =="
SEALED_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer $TOKEN_A" "$API/capsules/$CAP/memories")
[[ "$SEALED_CODE" == "403" ]]

upload_and_contribute() {
  local token="$1"
  local presign media_id upload_url memory_id
  presign=$(curl -sf -X POST "$API/media/presign" -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d '{"kind":"photo","content_type":"image/png","byte_size":67}')
  media_id=$(echo "$presign" | json_field "d['media_id']")
  upload_url=$(echo "$presign" | json_field "d['upload_url']")
  curl -sf -X PUT "$upload_url" -H 'Content-Type: image/png' --data-binary @"$PNG" >/dev/null
  curl -sf -X POST "$API/media/$media_id/confirm" -H "Authorization: Bearer $token" >/dev/null
  memory_id=$(curl -sf -X POST "$API/capsules/$CAP/memories" -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' -d "{\"media_id\":\"$media_id\"}" | json_field "d['id']")
  echo "$memory_id"
}

echo "== Contribute memories =="
upload_and_contribute "$TOKEN_A" >/dev/null
upload_and_contribute "$TOKEN_B" >/dev/null

echo "== Force freeze via SQL =="
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U memoria -d memoria -c \
  "UPDATE capsules SET state='frozen', frozen_at=now(), streak_perfect=false, last_contribution_at=now()-interval '8 days' WHERE id='$CAP';" >/dev/null

echo "== Unfreeze votes =="
curl -sf -X POST "$API/capsules/$CAP/unfreeze-vote" -H "Authorization: Bearer $TOKEN_A" >/dev/null
curl -sf -X POST "$API/capsules/$CAP/unfreeze-vote" -H "Authorization: Bearer $TOKEN_B" >/dev/null
curl -sf -X POST "$API/capsules/$CAP/unfreeze-vote" -H "Authorization: Bearer $TOKEN_C" >/dev/null

echo "== Force unlock (simulate unlock_capsules job) =="
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U memoria -d memoria -c \
  "UPDATE capsules SET state='unlocked', unlocked_at=now(), viewable_until=now()+interval '30 days' WHERE id='$CAP';" >/dev/null

echo "== View unlocked memories + react =="
MEM=$(curl -sf -H "Authorization: Bearer $TOKEN_A" "$API/capsules/$CAP/memories" | json_field "d['memories'][0]['id']")
URL=$(curl -sf -H "Authorization: Bearer $TOKEN_A" "$API/capsules/$CAP/memories" | json_field "d['memories'][0]['media_url']")
[[ -n "$URL" ]]
curl -sf -X PUT "$API/memories/$MEM/reactions" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d '{"emoji":"🎉"}' >/dev/null

echo "== Stats =="
curl -sf -H "Authorization: Bearer $TOKEN_A" "$API/capsules/$CAP/stats" | json_field "d['total_memories']" >/dev/null

echo "PASS: B6 exit test — full capsule lifecycle"
