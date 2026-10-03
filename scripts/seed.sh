#!/usr/bin/env bash
set -euo pipefail
API="${API:-http://localhost:8080}"

docker compose exec -T postgres psql -U space -d space -c \
  "INSERT INTO role_grants (user_id, role) SELECT id, 'admin' FROM users ON CONFLICT DO NOTHING;" >/dev/null

curl -sf -X POST "$API/v1/spaces" \
  -H "authorization: Bearer $ACCESS" -H 'content-type: application/json' -d '{
  "space_type":"apartment","rental_mode":"term","title":"2 Bedroom Apartment in East Legon",
  "description":"Quiet gated compound, borehole water, inverter backup, walking distance to the market.",
  "base_price_minor":450000,"currency":"GHS","price_period":"month",
  "deposit_minor":450000,"advance_months":12,"max_occupancy":4,
  "location":{"latitude":5.6363,"longitude":-0.1580,"locality":"East Legon","region":"Greater Accra","country":"GH","landmark":"Behind A&C Mall","digital_address":"GA-183-8164"},
  "amenities":["wifi","parking","gated_community"],
  "attributes":{"bedrooms":2,"bathrooms":2,"self_contained":true,"furnishing":"semi_furnished","kitchen":"private","water_source":"borehole","power_backup":"inverter"}
}' | jq -r '.id + "  " + .slug'
