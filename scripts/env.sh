#!/usr/bin/env bash
export API="${API:-http://localhost:8080}"

# Only mint a new token when the current one is dead. The OTP limit is 3 per 15
# minutes, so sourcing this twice must not cost two codes.
if [ -z "${ACCESS:-}" ] || ! curl -sf -o /dev/null -H "authorization: Bearer $ACCESS" "$API/v1/me"; then
  ACCESS=$(./scripts/token.sh) || { echo "no token" >&2; return 1 2>/dev/null || exit 1; }
  export ACCESS
  export REFRESH=$(jq -r '.refresh_token // empty' /tmp/space-token.json)
fi

if [ -z "${GUEST:-}" ] || ! curl -sf -o /dev/null -H "authorization: Bearer $GUEST" "$API/v1/me"; then
  GUEST=$(./scripts/token.sh 0209876543) || { echo "no guest token" >&2; return 1 2>/dev/null || exit 1; }
  export GUEST
fi


MINE=$(curl -s -H "authorization: Bearer $ACCESS" "$API/v1/spaces")
export SPACE=$(echo "$MINE" | jq -r '.spaces[0].id // empty')
export SLUG=$(echo "$MINE" | jq -r '.spaces[0].slug // empty')

echo "SPACE=${SPACE:-UNSET}  SLUG=${SLUG:-UNSET}"
