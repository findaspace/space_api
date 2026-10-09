#!/usr/bin/env bash
# Does not source .env or use an existing database.
set -euo pipefail
cd "$(dirname "$0")/.."
command -v docker >/dev/null || { echo 'Docker is required for isolated PostgreSQL integration tests.' >&2; exit 1; }
container="findaspace-integration-$$"
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT
# Random port prevents collisions with local development and other test runs.
docker run --rm -d --name "$container" -p 127.0.0.1::5432 \
 -e POSTGRES_USER=space -e POSTGRES_PASSWORD=integration-only \
 -e POSTGRES_DB=space postgis/postgis:18-3.6 >/dev/null
ready=false
for attempt in {1..60}; do
 if docker exec "$container" pg_isready -h 127.0.0.1 -U space -d space >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
if [[ "$ready" != true ]]; then docker logs "$container"; exit 1; fi
port=$(docker port "$container" 5432/tcp | sed -n 's/^127\.0\.0\.1://p')
[[ "$port" =~ ^[0-9]+$ ]] || { echo 'Could not determine the isolated PostgreSQL port.' >&2; exit 1; }
export SPACE_DATABASE_URL="postgres://space:integration-only@127.0.0.1:${port}/space?sslmode=disable"
export SPACE_DATABASE_DIRECT_URL="$SPACE_DATABASE_URL"
export SPACE_TEST_DATABASE_URL="$SPACE_DATABASE_URL"
go run ./cmd/migrate
# A second migration run must be harmless.
go run ./cmd/migrate
# Validate provisioning itself and the most sensitive grant boundaries.
docker exec -i "$container" psql -U space -d space -v ON_ERROR_STOP=1 < deploy/postgres/runtime-grants.sql
docker exec -i "$container" psql -U space -d space -v ON_ERROR_STOP=1 <<'SQL'
DO $$ BEGIN
 IF has_schema_privilege('space_runtime','public','CREATE')
 OR has_table_privilege('space_runtime','ledger_entries','UPDATE')
 OR has_table_privilege('space_runtime','ledger_entries','DELETE')
 OR has_table_privilege('space_runtime','trust_audit','UPDATE')
 OR has_table_privilege('space_runtime','trust_audit','DELETE')
 OR NOT has_table_privilege('space_runtime','user_blocks','INSERT')
 OR NOT has_table_privilege('space_runtime','sessions','SELECT')
 OR NOT has_table_privilege('space_runtime','river_job','INSERT') THEN
  RAISE EXCEPTION 'Runtime privilege boundary failed';
 END IF;
END $$;
SET ROLE space_runtime;
SELECT count(*) FROM sessions;
SELECT count(*) FROM river_job;
RESET ROLE;
SQL
go test -race -count=1 ./...
# Exercise a real backup and restore into another disposable database.
docker exec "$container" pg_dump -U space -d space -Fc -f /tmp/space-backup.dump
docker exec "$container" createdb -U space restored
docker exec "$container" pg_restore -U space -d restored --exit-on-error /tmp/space-backup.dump
export SPACE_TEST_DATABASE_URL="postgres://space:integration-only@127.0.0.1:${port}/restored?sslmode=disable"
go test -race -count=1 ./...
echo 'Disposable PostgreSQL tests and backup/restore checks passed.'
