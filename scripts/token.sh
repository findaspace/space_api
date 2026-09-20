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
