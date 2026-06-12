#!/usr/bin/env bash
# Phase B3 exit test: two users connect via invite token; block hides everything.
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

EMAIL_A="$(uniq b3a)@example.com"
EMAIL_B="$(uniq b3b)@example.com"
USER_A="$(uniq alice_)"
USER_B="$(uniq bob_)"

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

echo "== Invite link + connect =="
INVITE=$(curl -sf -X POST "$API/me/invite-link" -H "Authorization: Bearer $TOKEN_A" | json_field "d['token']")
curl -sf -H "Authorization: Bearer $TOKEN_B" "$API/users/by-invite/$INVITE" >/dev/null
curl -sf -X POST "$API/friends/requests" -H "Authorization: Bearer $TOKEN_B" \
  -H 'Content-Type: application/json' -d "{\"user_id\":\"$ID_A\"}" >/dev/null
curl -sf -X POST "$API/friends/requests/$ID_B/accept" -H "Authorization: Bearer $TOKEN_A" >/dev/null
COUNT=$(curl -sf -H "Authorization: Bearer $TOKEN_A" "$API/friends" | json_field "len(d['friends'])")
[[ "$COUNT" == "1" ]]

echo "== Block hides profile + requests =="
HTTP=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/blocks/$ID_B" -H "Authorization: Bearer $TOKEN_A")
[[ "$HTTP" == "204" ]]
HTTP=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN_B" "$API/users/$USER_A")
[[ "$HTTP" == "404" ]]
HTTP=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API/friends/requests" \
  -H "Authorization: Bearer $TOKEN_B" -H 'Content-Type: application/json' -d "{\"user_id\":\"$ID_A\"}")
[[ "$HTTP" == "404" ]]

echo "PASS: B3 exit test — connect via invite, block silent 404"
