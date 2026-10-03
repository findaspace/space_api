# Findaspace production setup and evidence

Production providers are not yet verified against Sam's accounts: no Neon, R2 or Arkesel credentials were available during this update. The real Go API/worker, PostgreSQL/PostGIS, two signed-in browsers and actual Ghana map data were verified locally. Local S3 checks use Moto, not Cloudflare R2. Do not label those results as a live deployment test.

## 1. Database: Neon PostgreSQL 18

Create a Neon project with **PostgreSQL 18**, in a region near the API/worker host. Existing migrations use PostgreSQL 18's `uuidv7()`; do not select 17. Keep application, worker and database in nearby regions. Copy the pooled URL to `SPACE_DATABASE_URL` and the direct URL to `SPACE_DATABASE_DIRECT_URL`, both with TLS. The API uses the pooled URL; migration and worker binaries prefer the direct URL. River uses LISTEN/NOTIFY, which transaction pooling does not support. The worker must be a continuously running process alongside the API.

Set `SPACE_DB_EXEC_MODE=exec` for conservative pooler compatibility. This update fixes both wiring and timing: the query mode and cache settings now reach the pool before it is created. Neon currently supports protocol-level prepared statements too; this option avoids dependence on a pooler's configuration. A 30-second connection ping timeout accommodates a cold compute start.

On an empty database or dedicated staging branch, load the secret environment and run:

```sh
go run ./cmd/migrate
go run ./cmd/verify -checks=db
```

The migration binary creates PostGIS, btree_gist, pg_trgm, citext and the River schema. The verification command only reads: it checks extensions, migration version 23+, a Ghana spatial query and the River table. Use a database backup/recovery point before upgrading an existing production database. Preserve existing authentication seeds and peppers when moving an existing deployment.

## 2. Photos and map tiles: Cloudflare R2

Neon supplies PostgreSQL; this project stores photos and PMTiles in an S3-compatible object store. Create an R2 bucket and bucket-scoped Object Read & Write credentials. Set the S3 endpoint, region `auto`, bucket, access/secret keys and path-style flag from `.env.production.example`.

Connect a public/custom media domain. `SPACE_STORAGE_PUBLIC_BASE_URL` must use that public domain, not the authenticated `r2.cloudflarestorage.com` endpoint. Listing images are public. Browser uploads use a signed PUT to the S3 API; processed images use the public domain. The worker needs read, write and delete access.

Replace the frontend-domain placeholder in `r2-cors-dashboard.json` and paste its contents into the bucket's dashboard CORS settings. `r2-cors.json` is the equivalent Wrangler format. Permit PUT/GET/HEAD, Content-Type/Range/If-Match and expose ETag/Content-Range/Content-Length/Accept-Ranges. Configure the public-domain/CDN path consistently too. R2 CORS does not grant public access by itself. If applying rules after caching objects, purge relevant cached responses before retesting.

Set `SPACE_VERIFY_WEB_ORIGIN` to the actual frontend origin and run:

```sh
go run ./cmd/verify -checks=storage
```

This creates one unique `_verification/*.png`, checks signed-upload preflight and actual PUT CORS, authenticated HEAD/GET, public GET/CORS and byte equality, then removes it. It never changes bucket permissions or CORS. A cleanup failure is reported. Confirm normal host upload/confirm/worker processing in the deployed frontend as well; the CLI probe alone cannot certify the whole photo workflow.

## 3. SMS: Arkesel

Create/fund an Arkesel account, register the `Findaspace` sender ID and store its API key in host secrets. Set provider `arkesel` and sandbox `false` in API and worker. The API's login path now uses the shared Arkesel implementation rather than rejecting all non-log providers. Production refuses log SMS, sandbox SMS, missing keys and sender IDs longer than 11 characters.

Set `SPACE_VERIFY_SMS_PHONE` to **your own test phone** in international format; then:

```sh
go run ./cmd/verify -checks=sms
```

This sends exactly one clearly labelled test SMS and reports provider acceptance. **Acceptance is not delivery.** Confirm arrival on the phone. Then use the deployed sign-in form to request a genuine OTP, enter the received code and verify `/account` opens. Do this with two test accounts, ideally on different Ghana mobile networks. Do not use development-log OTPs to claim real SMS delivery. Never include a real key or OTP in a bug report or saved QA artifact.

## 4. Ghana maps

The companion `findaspace-ghana-map.zip` contains a real Protomaps/OpenStreetMap PMTiles extract (build 20260929, zoom 0–14, bounding box -3.3,4.7,1.25,11.3). It covers Ghana plus a border margin. HTTP range requests fetch only the tiles required by the viewport; visitors do not download the full archive. This is a basemap, not a geocoder or a routing engine.

Upload `ghana.pmtiles` to `maps/ghana.pmtiles` in the bucket, content type `application/octet-stream`, without transforming or compressing the HTTP response. Set the frontend's `SPACE_MAP_TILES_URL` to its public HTTPS URL. Keep the bundled map license/attribution notice. The frontend bundles official Protomaps glyphs and sprites and the MapLibre worker, served from its own origin. Public-domain CORS must allow the frontend origin and byte-range requests; successful tile responses should be HTTP 206.

Run the frontend's `npm run test:maps` against the deployed site with `E2E_BASE_URL`, `SPACE_MAP_TILES_URL` and `MAP_LISTING_SLUG` (a published football-pitch listing). The test checks actual vector data, worker/glyph/sprite loading, phone and desktop views, marker cards, listing maps, attribution and recovery from a simulated tile failure. It only browses published listings and does not create accounts or change data.

## 5. Messaging and release

Chat is listing-based: a renter opens it using **Ask a question**, then both accounts reply in their inbox. It is not a general arbitrary-user direct-message directory. Visible conversations check for messages every five seconds and pause polling in hidden tabs. Threads persist across reloads. A third account cannot read/reply/mark another pair's conversation. Phone numbers stay intact in the direct-contact mode.

Messages and notifications now have separate 44px touch targets in a horizontal group, including narrow phones. Verify the deployed header after signing in. Payments remain disabled in both projects. Confirm demo mode is false. Save live-provider evidence only after the corresponding checks actually pass; no public deployment/push was performed as part of this source update.

Official references checked 30 September 2026:
- https://api-docs.neon.tech/reference/createproject
- https://neon.com/docs/connect/connection-pooling
- https://neon.com/docs/extensions/postgis
- https://developers.cloudflare.com/r2/buckets/cors/
- https://developers.arkesel.com/
- https://docs.protomaps.com/pmtiles/cloud-storage
- https://docs.protomaps.com/basemaps/downloads
