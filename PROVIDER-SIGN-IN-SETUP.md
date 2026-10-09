# Provider sign-in: deploy in this order

This package contains all previous Findaspace UI, PWA and reliability changes, plus Google, Microsoft, Apple, phone verification and explicit account linking. The two repositories remain separate.

## 1. Database and API

Run migration 00024 with the migration-owner/direct connection:

    go run ./cmd/migrate

Do NOT deploy this API against schema 23. Reapply deploy/postgres/runtime-grants.sql if using the separate runtime account; the new external identity and replay tables require grants. The service must not connect as the migration owner.

Migration 24 allows accounts without a phone, creates unique external identities and a token replay inbox, and enforces verified host contact before review/publication. Existing verified phone accounts retain their IDs. Rollback refuses if phone-less users exist; it does not delete accounts.

Set client IDs on the API, only for providers you enable:

    SPACE_GOOGLE_CLIENT_ID=
    SPACE_MICROSOFT_CLIENT_ID=
    SPACE_APPLE_CLIENT_ID=

These IDs must EXACTLY match the corresponding web client IDs. They are not client secrets. The API verifies provider signature, issuer, audience, expiry and nonce itself, using go-oidc v3.21.0 (current release checked). Microsoft common-authority responses are checked against their signed tenant-scoped issuer. Only the trusted web backend may exchange provider tokens. Its SPACE_BFF_SECRET must match the web.

## 2. Provider registrations and web environment

Use the actual HTTPS web origin in SPACE_SITE_URL, including on a preview deployment. The callback URLs below are based on https://findaspace.site; substitute the preview origin while testing and register its callbacks too.

Google: create a Web Application OAuth client and consent configuration. Register https://findaspace.site/auth/callback/google. Set SPACE_GOOGLE_CLIENT_ID and SPACE_GOOGLE_CLIENT_SECRET on the WEB SERVER.

Microsoft: register an application supporting both organizational directories and personal Microsoft accounts if you want work/school plus Outlook accounts. Add a Web redirect URI https://findaspace.site/auth/callback/microsoft. Set SPACE_MICROSOFT_CLIENT_ID and SPACE_MICROSOFT_CLIENT_SECRET on the WEB SERVER. Track the secret's expiry and rotate before it expires.

Apple: requires an Apple Developer setup with a Sign in with Apple primary App ID, associated Services ID, web domain and return URL https://findaspace.site/auth/callback/apple. Use the Services ID as SPACE_APPLE_CLIENT_ID. The web supports Apple's form_post response with an encrypted short-lived callback cookie and a same-site completion redirect. Set SPACE_APPLE_TEAM_ID, SPACE_APPLE_KEY_ID and SPACE_APPLE_PRIVATE_KEY (the downloaded .p8 PEM, as a server secret). Short-lived ES256 client-secret JWTs are generated automatically. A pre-generated SPACE_APPLE_CLIENT_SECRET is also supported, but it must be replaced before expiry. Never put the private key or any client secret in NEXT_PUBLIC variables, git or a shared ZIP.

Only configured methods appear in the UI. Phone remains an alternative; the form is expanded if no provider is available. Dummy credentials cannot make a real OAuth login work. Registering these applications and adding their credentials is still required; no live provider account was configured here.

## 3. Linking and contact verification

Users who already sign in by phone should use their existing phone login, open Account, then choose Connect Google/Microsoft/Apple. Connecting requires a session family whose original sign-in is less than 15 minutes old, plus fresh provider proof. Callback state is encrypted, expires after ten minutes, and is bound to the original account; switching accounts during the redirect rejects the link. Each provider identity can belong to only one account. A collision returns a conflict and preserves the original session.

Email is informational provider metadata. It is NEVER used to find, silently merge or move accounts. A person who independently creates accounts through different unlinked methods can still have separate accounts: proving that they represent one person requires an explicit linking process. We do not claim to deduplicate humans by email.

Provider-only users can browse, save, message and prepare drafts without a phone. Add a verified phone in Account before submitting a listing. SMS codes are single-use; requesting another code invalidates older pending codes. A phone already owned by another account cannot be reassigned or used for an implicit merge. SMS notifications skip users without a verified contact phone; in-app notifications remain.

## 4. Release checks

API: go vet ./...; golangci-lint run; go test -race ./...; sqlc generate. Generated models change because phone is nullable and the new tables exist. Commit the generated output with migration 24. Run make test-integration on a Docker-enabled machine: this covers real PostGIS, migrations, provider identity/linking boundaries, OTP attachment, host contact enforcement and backup/restore.

Web: Node 24.19.0; npm ci; npm run check; npm run build; npm run budget. Rebuild with real environment settings before deploying. Real Google/Microsoft/Apple consent, cancellation, new signup, repeated login, linking and expiry recovery must be tested on each configured provider. Test in normal Safari/Chrome AND the installed iPhone/Android PWA: provider redirects can differ between browser and installed-app contexts.

API lint, vet, race unit tests, provider signature/claims tests, sqlc generation and vulnerability scan passed locally. Web typecheck, lint, unit tests, production build, JavaScript budgets and structural accessibility checks passed locally. The Docker/PostGIS suite and real provider/device flows could not run here. Chromium crashes locally and its replacement download failed. No production migration, account registration, deployment or live backup/restore was performed.

Official references:
https://developers.google.com/identity/openid-connect/openid-connect
https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc
https://developer.apple.com/help/account/capabilities/configure-sign-in-with-apple-for-the-web/
https://developer.apple.com/documentation/signinwithapple/configuring-your-webpage-for-sign-in-with-apple
