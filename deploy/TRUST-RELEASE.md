# Trust controls and release gates

API builds are pinned to Go 1.27.2 (security release published 8 October 2026; https://go.dev/doc/devel/release#go1.27.2). This replaces the unpinned Go 1.26 Docker tag and Go 1.26.0 minimum.

This update includes migration 00025, reporting, a staff review queue, communication blocking, account closure, durable object cleanup and optional Prometheus alert rules. The previously scheduled video-upload sweeper is now registered, with a regression check for both cleanup job kinds. Google, Apple and Microsoft implementation from the preceding bundle is retained. It still requires real provider registration and keys; buttons appear only for configured providers.

## Required deployment order

1. Run `make test-integration` on a machine with Docker. It creates disposable PostGIS 18, migrates twice, checks runtime grants, runs the real database tests, and performs a dump/restore followed by another test run. This environment could not run Docker/PostGIS; that gate is outstanding.
2. Take a hosted database backup. Test restoration into an isolated database. Never point an integration suite at production.
3. As the dedicated migration owner, apply migrations through **00025_trust_controls.sql**. Do not use the API runtime account for migration.
4. Apply `deploy/postgres/runtime-grants.sql` as the owner. New tables: listing_reports, user_blocks, storage_deletions, trust_audit. Audit is SELECT/INSERT only.
5. Deploy API and worker together, then deploy web. Run the worker; object deletion is not complete without it. Cleanup waits 24 hours for upload links and in-flight media jobs, then retries failed object deletion. Upload URLs must expire within one hour. Existing externally cached media needs the storage/CDN's purge policy; origin deletion cannot retract downloaded files.
6. Give nominated staff `admin` or `support` via the existing controlled role-provisioning procedure. The API checks roles from the database. Staff review is `/staff/reports`. Reports are private and do not auto-remove listings. Staff can explicitly withdraw a listing when resolving a report; that change and its audit event commit together. Agree who handles the review queue.
7. Publish the operator's real legal identity, staffed support contact and approved retention/privacy terms. The product describes controls but does not invent those facts.

## Gate evidence to collect in staging

- Google, Apple and Microsoft: new sign-in, cancel, denied consent, wrong/mismatched identity token, callback in a fresh tab, installed-app callback, existing phone user explicitly linking, attempted cross-account linking, logout and subsequent revoked-session rejection. Follow PROVIDER-SIGN-IN-SETUP.md.
- Session: reload, switch away for 10 minutes, cross-tab use, access-token refresh, temporary API outage preserving sign-in, sign-out invalidating private access. Never test with a copied access token in localStorage.
- Real SMS: one consented tester receives a code, bad/expired codes fail, resend is rate limited, no OTP codes appear in production logs. Check billing and sandbox disabled.
- Storage and workers: real photo upload/confirmation/processing, invalid MIME/oversize rejection, video processing, abandoned uploads, restarted worker retries. With a disposable account, closure hides listings immediately and cleanup eventually removes raw/card/video/poster object keys. Verify the storage_deletions queue, including retry after a storage outage.
- Trust: report twice produces one open report; nonstaff review is rejected; nonparticipant cannot block; both message directions and another listing's thread respect a block; new contact reveals stop; unblock removes only your own block. Account deletion requires fresh sign-in, revokes all devices, and refuses active bookings or staff roles.
- Postgres concurrency and restoration: execute the full real integration suite. A passing unit suite alone is not this evidence.
- Load/cold start: measure first anonymous page load after idle and repeated loads. Serve the Next app and API on instances that meet your availability target. The streaming shell reduces perceived waiting; it cannot remove a hosting cold start.

## Monitoring wiring

Structured API logs and metrics already exist. `deploy/prometheus/alerts.yml` supplies API-up, 5xx ratio, p95 latency and pool-pressure rules using emitted metrics. Import them into a Prometheus-compatible monitor, configure its scrape of the private API metrics listener, configure Alertmanager/contact delivery, then trigger a staging failure and prove receipt. Merely committing rules does not activate alerts.

Web adds sanitized `web_server_error` and `web_client_error` JSON events. They contain a route category and an optional support digest, not raw URLs, queries, phone numbers, tokens, error messages or bodies. Connect hosting logs to your log monitor and alert on these events. `/api/client-errors` accepts only same-origin JSON, limits bodies to 1 KiB and caps each process at 60 submissions/minute; configure a CDN rate limit as well. Error telemetry is observational, not authorization.

Useful owner-only queue check:

```sql
SELECT count(*) AS overdue_cleanup
FROM storage_deletions
WHERE deleted_at IS NULL AND not_before < now() - interval '2 hours';
```

Investigate a nonzero result: worker stopped, credentials missing, storage unavailable, or permissions incorrect. Do not clear pending rows to silence an alert.

Saved-search notifications and deeper availability/freshness automation remain separate feature work. They require notification consent, a delivery channel and explicit listing expiry policy. They are not disguised as completed launch checks in this bundle.
