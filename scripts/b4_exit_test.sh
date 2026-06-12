#!/usr/bin/env bash
# Phase B4 exit test: friend request triggers outbox flush → in-app notification.
# Real device push requires Expo credentials + dev app; this script verifies
# the server-side pipeline end-to-end against docker compose.
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

EMAIL_A="$(uniq b4a)@example.com"
EMAIL_B="$(uniq b4b)@example.com"
USER_A="$(uniq alice_)"
USER_B="$(uniq bob_)"
EXPO_TOKEN="ExponentPushToken[b4-exit-test-$(date +%s)]"

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

echo "== Register push token for A =="
curl -sf -X PUT "$API/me/push-token" -H "Authorization: Bearer $TOKEN_A" \
  -H 'Content-Type: application/json' \
  -d "{\"token\":\"$EXPO_TOKEN\",\"platform\":\"ios\"}" >/dev/null

echo "== B sends friend request to A =="
curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$ID_A\"}" >/dev/null

echo "== Wait for outbox flush (30s cron or immediate on enqueue) =="
sleep 2

echo "== Verify in-app notification for A =="
COUNT=$(curl -sf -H "Authorization: Bearer $TOKEN_A" "$API/notifications" | json_field "len(d['notifications'])")
[[ "$COUNT" == "1" ]]

UNREAD=$(curl -sf -H "Authorization: Bearer $TOKEN_A" "$API/notifications/unread-count" | json_field "d['count']")
[[ "$UNREAD" == "1" ]]

CATEGORY=$(curl -sf -H "Authorization: Bearer $TOKEN_A" "$API/notifications" | json_field "d['notifications'][0]['category']")
[[ "$CATEGORY" == "friend_requests" ]]

echo "PASS: B4 exit test — friend request → outbox sent → in-app feed (device push needs real Expo + dev app)"
