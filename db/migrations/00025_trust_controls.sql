-- +goose Up
CREATE TABLE listing_reports (
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 reporter_id uuid NOT NULL REFERENCES users(id),
 space_id uuid NOT NULL REFERENCES spaces(id),
 reason text NOT NULL CHECK (reason IN ('suspected_scam','unavailable','incorrect_details','abuse','other')),
 details text NOT NULL DEFAULT '' CHECK (char_length(details)<=2000),
 status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved','dismissed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 reviewed_at timestamptz,
 reviewer_id uuid REFERENCES users(id),
 review_note text NOT NULL DEFAULT '' CHECK (char_length(review_note)<=2000)
);
CREATE UNIQUE INDEX listing_reports_open_unique ON listing_reports(reporter_id,space_id) WHERE status='open';
CREATE INDEX listing_reports_queue ON listing_reports(created_at,id) WHERE status='open';
CREATE TABLE user_blocks (
 blocker_id uuid NOT NULL REFERENCES users(id),
 blocked_id uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(blocker_id,blocked_id), CHECK(blocker_id<>blocked_id)
);
CREATE INDEX user_blocks_target ON user_blocks(blocked_id,blocker_id);
CREATE TABLE trust_audit (
 id uuid PRIMARY KEY DEFAULT uuidv7(),
 actor_id uuid NOT NULL,
 action text NOT NULL CHECK (action IN ('listing_reported','report_reviewed','listing_withdrawn','user_blocked','user_unblocked','account_deleted')),
 subject_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE storage_deletions (
 object_key text PRIMARY KEY,
 not_before timestamptz NOT NULL DEFAULT now()+interval '24 hours',
 created_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz
);
CREATE INDEX storage_deletions_due ON storage_deletions(not_before) WHERE deleted_at IS NULL;
-- +goose StatementBegin
CREATE FUNCTION reject_trust_audit_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'trust audit is append only' USING ERRCODE='23514'; END;
$$;
CREATE TRIGGER trust_audit_immutable BEFORE UPDATE OR DELETE ON trust_audit FOR EACH ROW EXECUTE FUNCTION reject_trust_audit_change();

-- A shared pair lock serialises blocking with new messages. Neither direction
-- can evade a block, including another conversation for a different listing.
CREATE FUNCTION guard_user_block() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE a uuid; b uuid;
BEGIN
 IF TG_OP='DELETE' THEN a:=OLD.blocker_id; b:=OLD.blocked_id;
 ELSE a:=NEW.blocker_id; b:=NEW.blocked_id; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(least(a::text,b::text)||':'||greatest(a::text,b::text),0));
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER user_blocks_pair BEFORE INSERT OR DELETE ON user_blocks FOR EACH ROW EXECUTE FUNCTION guard_user_block();
CREATE FUNCTION guard_message_participants() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE guest uuid; host uuid; live_count integer;
BEGIN
 SELECT guest_id,host_id INTO guest,host FROM threads WHERE id=NEW.thread_id;
 IF NOT FOUND OR NEW.sender_id NOT IN (guest,host) THEN
  RAISE EXCEPTION 'not a conversation participant' USING ERRCODE='23514',CONSTRAINT='messages_participant';
 END IF;
 -- Lock users in a fixed order so account closure cannot race a message.
 PERFORM id FROM users WHERE id IN (guest,host) ORDER BY id FOR SHARE;
 PERFORM pg_advisory_xact_lock(hashtextextended(least(guest::text,host::text)||':'||greatest(guest::text,host::text),0));
 SELECT count(*) INTO live_count FROM users WHERE id IN (guest,host) AND status='active';
 IF live_count<>2 OR EXISTS(SELECT 1 FROM user_blocks WHERE (blocker_id=guest AND blocked_id=host) OR (blocker_id=host AND blocked_id=guest)) THEN
  RAISE EXCEPTION 'messaging unavailable' USING ERRCODE='23514',CONSTRAINT='messages_blocked';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER messages_participants BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION guard_message_participants();
-- Refuse late uploads after an account was closed, even when the HTTP
-- request passed its session check before the closure committed.
CREATE FUNCTION guard_media_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner uuid; active_owner uuid;
BEGIN
 SELECT host_id INTO owner FROM spaces WHERE id=NEW.space_id;
 SELECT id INTO active_owner FROM users WHERE id=owner AND status='active' FOR SHARE;
 IF active_owner IS NULL THEN RAISE EXCEPTION 'owner is unavailable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER media_live_owner BEFORE INSERT ON space_media FOR EACH ROW EXECUTE FUNCTION guard_media_owner();
CREATE TRIGGER video_live_owner BEFORE INSERT ON space_videos FOR EACH ROW EXECUTE FUNCTION guard_media_owner();
CREATE FUNCTION guard_space_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner uuid;
BEGIN
 SELECT id INTO owner FROM users WHERE id=NEW.host_id AND status='active' FOR SHARE;
 IF owner IS NULL THEN RAISE EXCEPTION 'owner is unavailable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER spaces_live_owner BEFORE INSERT OR UPDATE ON spaces FOR EACH ROW EXECUTE FUNCTION guard_space_owner();
CREATE FUNCTION guard_booking_accounts() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE live_count integer;
BEGIN
 IF NEW.status IN ('held','approved','confirmed','active') THEN
  PERFORM id FROM users WHERE id IN (NEW.host_id,NEW.guest_id) ORDER BY id FOR SHARE;
  SELECT count(*) INTO live_count FROM users WHERE id IN (NEW.host_id,NEW.guest_id) AND status='active';
  IF live_count<>2 THEN RAISE EXCEPTION 'booking account is unavailable' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER bookings_live_accounts BEFORE INSERT OR UPDATE OF status ON bookings FOR EACH ROW EXECUTE FUNCTION guard_booking_accounts();
CREATE FUNCTION guard_contact_boundary() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(least(NEW.viewer_id::text,NEW.host_id::text)||':'||greatest(NEW.viewer_id::text,NEW.host_id::text),0));
 IF EXISTS(SELECT 1 FROM user_blocks WHERE (blocker_id=NEW.viewer_id AND blocked_id=NEW.host_id) OR (blocker_id=NEW.host_id AND blocked_id=NEW.viewer_id)) THEN
  RAISE EXCEPTION 'contact unavailable' USING ERRCODE='23514',CONSTRAINT='contact_blocked';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER contact_boundary BEFORE INSERT ON contact_reveals FOR EACH ROW EXECUTE FUNCTION guard_contact_boundary();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER contact_boundary ON contact_reveals;
DROP FUNCTION guard_contact_boundary();
DROP TRIGGER bookings_live_accounts ON bookings;
DROP FUNCTION guard_booking_accounts();
DROP TRIGGER spaces_live_owner ON spaces;
DROP FUNCTION guard_space_owner();
DROP TRIGGER media_live_owner ON space_media;
DROP TRIGGER video_live_owner ON space_videos;
DROP FUNCTION guard_media_owner();
DROP TRIGGER messages_participants ON messages;
DROP FUNCTION guard_message_participants();
DROP TABLE storage_deletions;
DROP TABLE trust_audit;
DROP FUNCTION reject_trust_audit_change();
DROP TABLE user_blocks;
DROP FUNCTION guard_user_block();
DROP TABLE listing_reports;
