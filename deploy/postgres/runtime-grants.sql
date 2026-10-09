-- Run as the migration owner after migrations. This group cannot log in.
-- Create a separate LOGIN account with a password outside source control, then
-- GRANT space_runtime TO that account. Do not use the table owner for the API.
BEGIN;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='space_runtime') THEN
  CREATE ROLE space_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
 END IF;
END $$;
GRANT USAGE ON SCHEMA public TO space_runtime;
REVOKE CREATE ON SCHEMA public FROM PUBLIC, space_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON
 users, role_grants, otp_challenges, sessions, external_identities, external_login_exchanges, spaces, space_media,
 stay_rules, rate_plans, availability_blocks, bookings,
 payments, webhook_events, payout_accounts, payouts,
 threads, messages, reviews, notifications, notification_deliveries,
 notification_preferences, space_videos, space_landmarks, contact_reveals, listing_reports, user_blocks, storage_deletions
 TO space_runtime;
GRANT SELECT, INSERT ON trust_audit TO space_runtime;
GRANT SELECT ON landmarks, spatial_ref_sys TO space_runtime;
GRANT SELECT, INSERT ON ledger_transactions, ledger_entries TO space_runtime;
-- River adds versioned internal tables. Limit grants to its known prefix;
-- never grant the runtime migration/version-table access or schema ownership.
DO $$ DECLARE item record; BEGIN
 FOR item IN SELECT tablename FROM pg_tables
  WHERE schemaname='public' AND tablename LIKE 'river\_%' ESCAPE '\'
   AND tablename <> 'river_migration'
 LOOP
  EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.%I TO space_runtime',item.tablename);
 END LOOP;
 FOR item IN SELECT sequencename FROM pg_sequences
  WHERE schemaname='public' AND sequencename LIKE 'river\_%' ESCAPE '\'
 LOOP
  EXECUTE format('GRANT USAGE, SELECT ON SEQUENCE public.%I TO space_runtime',item.sequencename);
 END LOOP;
END $$;
COMMIT;
