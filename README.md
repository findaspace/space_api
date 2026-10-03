# Findaspace / space_api

Go API and background worker for a Ghana space marketplace. The companion `space_web` project provides the phone-first black-and-white interface. This version defaults to direct contact: phone-authenticated renters can reveal the contact for published spaces and send enquiries; platform payments are off.

## Local development

Requires Go 1.26+, PostgreSQL 18 with PostGIS, an S3-compatible store, and FFmpeg/FFprobe for video processing. The supplied Docker Compose starts PostgreSQL, MinIO and optional OSRM. For the essential dependencies only:

```sh
cp .env.example .env
# Fill SPACE_OTP_PEPPER and SPACE_JWT_SEED with separate 32-byte base64 values;
# fill SPACE_BFF_SECRET with a random 32+ character value shared with space_web.
docker compose up -d postgres minio
make migrate
make run
# In another terminal:
make worker
```

The development `log` SMS provider writes OTPs to the local log and does not send real SMS. Production rejects it. Configure storage CORS for the frontend origin, PUT and Content-Type. Local API mode creates its bucket; production storage should be provisioned explicitly. Registry access for the original MinIO image could not be established in the build environment, so the connected tests used a local Moto S3 emulator. Keep production on an appropriately configured S3-compatible service.

The frontend's `SPACE_API_URL` points to this API, and `SPACE_BFF_SECRET` must match. Set `SPACE_DIRECT_CONTACT=true` on the API and `SPACE_PAYMENTS_ENABLED=false` on both projects. Run the frontend with its README instructions. No demo API should be deployed.

## Capabilities added or connected

- Football pitches, sports courts, general sports facilities and studios, with schema-defined attributes.
- Flexible rental mode with hour/day/week rates; semester/academic-year rates for hostel beds. Existing monthly/yearly and night/week periods remain.
- Search by exact `price_period`, keeping budget comparisons in the selected unit.
- Private GET, core PATCH, withdrawal and photo delete/order operations documented in OpenAPI and connected to the host frontend.
- Core edits return a listing to draft. Withdraw a live listing before editing; send edited drafts for review again. Slugs remain stable when titles change.
- Input validation reports invalid details as 422 rather than relying on database CHECK failures.
- Direct-contact messages retain contact details without a paid booking. The reveal endpoint is now wired to the running API, with a transactional unique-space daily cap. Repeated reveals remain audited but don't use another allowance.
- Upload reservations enforce photo limits under a row lock. Publication and deletion protect the minimum ready photo. Reordering validates the complete ready-photo set.
- Payment routes return 404 when payments are disabled; Paystack configuration is not required for the direct-contact product.

## Migrations and contract

Apply migrations before starting the new API. `00022` adds enum values outside a transaction; `00023` changes type/mode/period constraints. Existing records are preserved. PostgreSQL cannot simply remove enum values on rollback; reclassify new inventory before rolling back the pricing constraints/application. Back up the database before deployment.

`api/openapi.yaml` is the contract. SQL types are generated with sqlc 1.31.1 from `db/queries` and `db/migrations`; `make generate` regenerates them. Copy the contract into the frontend and run `npm run api:types` after future changes. The current release already contains matching generated types.

## Verification

```sh
go vet ./...
go build ./...
go test -race -count=1 ./...
SPACE_TEST_DATABASE_URL=postgres://space:space@localhost:5432/space?sslmode=disable go test -race -count=1 ./...
npx --yes @redocly/cli lint api/openapi.yaml
```

The database must be migrated before integration tests. CI sets the integration URL explicitly. The migration command requires only `SPACE_DATABASE_URL` and exits nonzero on failure.

The companion frontend includes `tests/e2e/real-api.mjs` for local connected checks against this API, PostgreSQL, the real River worker and S3 storage. It uses development OTP logs and fixture admin grants and refuses remote hosts; never run it against production. Frontend screenshots/results and backend test evidence are included in the deliverables.

## Deployment

Build explicit targets: `docker build --target api -t space_api .` and `docker build --target worker -t space_api-worker .`. The default target is API. The corrected workflow publishes separate images; the production compose has one API and one worker definition. Adapt image paths if forking the repository. The final release workflow prints a manual deployment command; it does not connect to your server.

Use HTTPS, real Arkesel credentials, enabled rate limiting, explicit CORS origins, production storage and matching BFF secrets. Keep payments disabled for this launch. Existing payment engines do not price hourly, daily or academic-period bookings; those quote paths reject unsupported periods rather than silently treating them as nights/months. The first release still uses direct contact to arrange rental terms.

Host verification evidence, viewing scheduling, reporting workflows and account-synchronised favourites remain future product work. No ownership-verification badge is inferred from phone verification or a listing declaration. See the companion frontend's updated `BACKEND_REQUIREMENTS.md` for those remaining boundaries.

## Production provider checks

See `deploy/PRODUCTION_SETUP.md` and `.env.production.example`. The API now supports Arkesel OTP delivery through the same sender used by the worker. Migrations and the River worker prefer `SPACE_DATABASE_DIRECT_URL`; the API uses `SPACE_DATABASE_URL`. Database execution/cache settings are now actually applied before the pool is created.

`go run ./cmd/verify -checks=db,storage` reads database readiness and creates/removes one unique storage probe object, including real upload/public-domain CORS checks. `-checks=sms` sends one test SMS only when explicitly selected with a configured recipient; confirm handset delivery separately. No production credentials were available for this update, so live account verification remains pending.

## Cheapest Render test deployment

Use `render.yaml` and `Dockerfile.render-test` to run API and one photo worker in a single Free web service. The frontend has its own Free-service Blueprint. Follow `deploy/RENDER_TEST_SETUP.md`; apply migrations from your machine first. Both services sleep when idle and share Render's free-hour allowance. This mode targets a small test deployment. Keep the existing separate API/worker Docker targets for an always-on launch.
