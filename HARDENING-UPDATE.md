# Findaspace reliability and access update

Keep API and web as separate repositories. No hosting or domain changes are included.

## API changes

Authenticated requests verify live session revocation, active account status, and current roles. Token claims no longer decide privileges. Database errors fail closed with 503, rather than pretending the session is invalid. Private responses are explicitly no-store. A rotated refresh session remains usable for already-issued access tokens; logout revokes its entire family.

Goose uses a PostgreSQL session advisory lock with a bounded wait. Use a direct connection or session pooler for migrations, not a transaction pooler. River has its own migration locking. Transaction rollback has a five-second cleanup deadline even if the request was cancelled.

`make test-integration` (or `make itest`) now creates an isolated PostGIS 18 container on a random localhost port, applies migrations twice, runs every race test including refresh concurrency checks, restores a real pg_dump into a second database, and repeats the tests there. Docker is required. It never uses `.env` database credentials or an existing database. The container is removed on exit.

## Database accounts

This combined provider-sign-in update REQUIRES migration 00024 before deploying the API. Read PROVIDER-SIGN-IN-SETUP.md. Keep migration-owner credentials separate from API/worker credentials. `deploy/postgres/runtime-grants.sql` is an explicit, optional provisioning script for the existing public-schema installation, to run after migrations as its owner. It creates a NOLOGIN permission group, not a password-bearing account. Attach a separate runtime LOGIN to it. Do not run the service as the table owner, superuser, or a role inheriting either. The runtime cannot create tables or edit/delete ledger entries. Reapply grants when new tables arrive. New grants must be checked against actual River version and job flows before switching a live service; this script has not been exercised in this workspace.

## Additional fixes

The missing authenticated `PATCH /v1/me` route is registered with the write limiter, so account name changes can reach their existing handler. OTP throttling now reaches its Retry-After branch. The media worker rejects oversized downloaded videos even when the object was replaced after its HEAD size check. Close/cleanup handling and several static-analysis findings were corrected without disabling lint rules.

## Verified here

Go 1.27.2 build (updated for the 8 October security release), gofmt, go vet, golangci-lint 2.14.0 (zero issues), race unit tests, sqlc 1.31.1 generation with no diff, and govulncheck 1.8.0 (no known vulnerabilities found). Database-dependent tests skip without a database and are NOT included in that pass claim. The newest session query checks account, session and roles in one database round trip without a privilege cache.

## Verification limits

The local workspace has no Docker, no PostGIS installation and does not permit switching to a non-root PostgreSQL process. Database integration tests therefore cannot be claimed passed here. Run `make test-integration` on a Docker-enabled machine before release. The existing GitHub CI already migrates and tests a PostGIS 18 service, but this update has not been pushed or run on GitHub.

SMS delivery, S3 upload permissions, live worker processing, real browser/device flows, runtime-account provisioning and load under concurrent traffic still require connected checks. Passing unit tests is not a complete security assessment. No production backup or restore was performed.
