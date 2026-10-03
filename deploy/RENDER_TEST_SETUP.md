# Cheapest Findaspace test deployment on Render

This setup creates **two free web services**, not a paid background-worker service. The API container also runs one River worker, so photo upload/processing continues to function while the API is awake. The separate worker/API production Docker targets remain available for a later always-on launch.

## Cost and limits

Use Render Free for the frontend and combined API/worker, Neon Free for PostgreSQL, and R2 Standard's included free usage for photos/map tiles. This aims for zero hosting cost while staying within all free allowances. Real Arkesel SMS needs credits. R2 storage/operation overages and any optional paid upgrades are separate costs; this is not a promise of unlimited free hosting.

Render's free web services sleep after 15 idle minutes and can take about a minute to wake. The workspace's **750 monthly free instance hours are shared by both services**. Do not add artificial keep-alive traffic. An initial request may require waiting and retrying. Photo jobs pause when the combined container sleeps; their durable queue is in Neon, and the worker resumes when the API is awake. This is acceptable for a small test deployment, not an always-on launch. Background polling also consumes Neon compute while the container is active.

No paid domain is required. Use the actual Render onrender.com URLs and, for this test, the bucket's public r2.dev URL. R2's r2.dev access is rate-limited and intended for development; move to a custom media domain for a public production launch.

## 1. Prepare accounts and secrets

Create Neon PostgreSQL **18**, an R2 Standard bucket and an Arkesel account with a registered sender ID and SMS credits. Select a Neon region near Render Frankfurt. Follow the API package’s `deploy/PRODUCTION_SETUP.md` for credentials, CORS and provider tests. Upload the supplied Ghana archive to `maps/ghana.pmtiles`.

Generate separate OTP pepper/JWT seed values with `openssl rand -base64 32` and a BFF secret with `openssl rand -hex 32`. Store values in Render environment settings, never in Git. Keep peppers/seeds unchanged if moving existing users. The BFF secret must be identical in the frontend and API.

## 2. Migrate from your machine

Render Free does not offer the paid pre-deploy/shell features. Apply migrations from your machine before the first deploy and before updates containing new migrations:

```sh
# Load your private environment first; don't put the URL literally into shell history.
go run ./cmd/migrate
go run ./cmd/verify -checks=db,storage
```

`SPACE_DATABASE_DIRECT_URL` is used by migrations and the worker; `SPACE_DATABASE_URL` is the pooled API URL. The container intentionally does not apply migrations on every cold start. `/ready` checks database connectivity; migration readiness is checked separately by `cmd/verify`.

## 3. Deploy the API repository

Place the contents of `space_api/` at the repository root. Render's **New → Blueprint** can read its `render.yaml`; choose the API repository and fill the fields marked `sync: false`. It explicitly selects the Free plan and `Dockerfile.render-test`. There is no separate Render worker service.

If using New → Web Service manually:

- Runtime: Docker
- Plan: Free
- Dockerfile: `./Dockerfile.render-test`
- Region: Frankfurt
- Health path: `/ready`
- Environment: use `.env.production.example`; set `SPACE_WORKER_CONCURRENCY=1`, `SPACE_DB_MAX_CONNS=3`, `SPACE_DB_MIN_CONNS=1`.

The entrypoint binds the API to Render's `PORT`, starts API and worker, stops both on termination and fails the container if either child exits. Image processing uses one worker at a time. The paid/standalone worker still defaults to ten unless overridden.

Record the actual public API URL shown by Render. Service names/URLs may differ from the example Blueprint if a name is already taken.

## 4. Deploy the frontend repository

Place the contents of `space_web/` at the frontend repository root. Use its `render.yaml` Blueprint or create a Free Node web service with:

- Build: `npm ci --include=dev && npm run build`
- Start: `npm run start -- --hostname 0.0.0.0 --port $PORT`
- Node: 24
- Region: Frankfurt

Set `SPACE_API_URL` to the API's **public HTTPS URL**. A free API cannot receive private-network traffic, so do not use Render's internal hostname here. Set `SPACE_SITE_URL` to the actual frontend HTTPS URL, the shared `SPACE_BFF_SECRET`, `SPACE_MAP_TILES_URL` to the public Ghana archive URL, `SPACE_DEMO_MODE=false` and `SPACE_PAYMENTS_ENABLED=false`. The Blueprint allows 90 seconds for API requests to accommodate an API cold start; the first visit can still need a retry.

Update API `SPACE_CORS_ORIGINS`, storage bucket CORS origins and `SPACE_VERIFY_WEB_ORIGIN` to the actual frontend origin. No guessed example URL should remain.

## 5. Verify the real deployment

Wake the API by visiting `/health`, wait until `/ready` returns 200, then open the frontend. Run explicit provider probes from your configured machine; real SMS acceptance still requires confirming receipt on your test phone. Sign in with two accounts, post a photo, wait for worker processing, publish a test listing through an admin, check the Ghana map and exchange messages. Do not run local fixture role-grant/OTP-log scripts against this deployment.

The frontend's `test:maps` is read-only and can check the deployed site's published sports listing. Do not claim a live deployment or live SMS verification until these checks actually happen.

This update validates the Blueprint files and the process lifecycle locally. Render image deployment and free-instance memory/performance still require a real deploy; Docker was unavailable in the authoring environment.

Official references checked 30 September 2026:
- https://render.com/docs/free
- https://render.com/docs/blueprint-spec
- https://render.com/docs/deploy-nextjs-app
- https://neon.com/pricing
- https://developers.cloudflare.com/r2/pricing/
- https://developers.cloudflare.com/r2/buckets/public-buckets/
