#!/usr/bin/env bash
set -euo pipefail

PHONE="${1:-0244123456}"
API="${API:-http://localhost:8080}"

curl -sf -X POST "$API/v1/auth/otp" \
  -H 'content-type: application/json' \
  -d "{\"phone\":\"$PHONE\"}" >/dev/null

read -rp "code from the api log: " CODE

curl -sf -X POST "$API/v1/auth/verify" \
  -H 'content-type: application/json' \
  -d "{\"phone\":\"$PHONE\",\"code\":\"$CODE\"}" > /tmp/space-token.json

jq -r .access_token /tmp/space-token.json


OTP=$(curl -s -w '\n%{http_code}' -X POST "$API/v1/auth/otp" \
  -H 'content-type: application/json' -d "{\"phone\":\"$PHONE\"}")

if [ "$(echo "$OTP" | tail -1)" != "202" ]; then
  echo "otp request failed:" >&2
  echo "$OTP" | head -1 >&2
  exit 1
fi
