-- 0142: one handle namespace for accounts and channels, and the block model
-- that follows from it (A29 parity).
--
-- WHAT THE REHEARSAL MEASURED. `federation.WebFinger` resolves a name@domain to
-- the USER first and only then to a channel, and NOTHING prevented a channel
-- taking a handle an account already holds — creating channel `ownera` while
-- user `ownera` exists answered 201. On such an instance every channel-scoped
-- federation feature keyed on that handle silently addresses the wrong actor: a
-- remote viewer blocking `@ownera@host` stored `/accounts/ownera` while the
-- videos are attributed to `/video-channels/ownera`, so the block hid nothing,
-- and a remote Follow of the handle queued a Follow of a Person that the inbox
-- drops with no record and no Reject. PeerTube does not have this problem
-- because an account and a channel share one namespace there; this migration
-- adopts that model.
--
-- (1) actor_handles — THE RESERVATION. One row per live handle, whatever kind of
-- actor holds it, with the lower-cased handle as the PRIMARY KEY. That single
-- key is the whole enforcement: a channel cannot take a username and a username
-- cannot take a channel handle, in the DATABASE, on every write path including
-- the ones that do not exist yet (there is no username-change endpoint today and
-- the channel handle is immutable — the triggers still cover both, so whichever
-- ships first cannot ship the hole).
--
-- Normalisation is `lower()`, the same normalisation `users_username_lower_idx`
-- and `channels_handle_lower_idx` have always used, so the reservation cannot
-- disagree with the per-table uniqueness that predates it.
CREATE TABLE actor_handles (
    handle_lower TEXT        PRIMARY KEY,
    -- Exactly one of these is set, and each is a CASCADING foreign key: deleting
    -- an account or a channel releases its handle without a line of application
    -- code, and without this migration ever issuing a DELETE.
    user_id      UUID        REFERENCES users (id)    ON DELETE CASCADE,
    channel_id   UUID        REFERENCES channels (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT actor_handles_one_subject CHECK (num_nonnulls(user_id, channel_id) = 1)
);

-- "which handle does this subject hold?" — also the ON CONFLICT arbiter each
-- trigger uses to MOVE a subject's reservation when its name changes.
CREATE UNIQUE INDEX actor_handles_user_idx    ON actor_handles (user_id)    WHERE user_id    IS NOT NULL;
CREATE UNIQUE INDEX actor_handles_channel_idx ON actor_handles (channel_id) WHERE channel_id IS NOT NULL;

-- The triggers are AFTER row triggers on purpose. A same-kind duplicate (a user
-- taking another user's username) must keep raising on
-- users_username_lower_idx, which is checked at INSERT time — before any AFTER
-- trigger runs — so the existing 409s and their messages are unchanged. Only a
-- CROSS-kind collision reaches actor_handles_pkey, and that is the one the API
-- renders as the new `handle_reserved` conflict.
--
-- Each upsert arbitrates on the SUBJECT index, so a rename moves the existing
-- reservation; a collision on handle_lower is a different index and is therefore
-- raised rather than swallowed, which is the entire point.
CREATE OR REPLACE FUNCTION reserve_account_handle() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND lower(NEW.username) = lower(OLD.username) THEN
        RETURN NEW;
    END IF;
    INSERT INTO actor_handles (handle_lower, user_id)
    VALUES (lower(NEW.username), NEW.id)
    ON CONFLICT (user_id) WHERE user_id IS NOT NULL
    DO UPDATE SET handle_lower = EXCLUDED.handle_lower;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION reserve_channel_handle() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND lower(NEW.handle) = lower(OLD.handle) THEN
        RETURN NEW;
    END IF;
    INSERT INTO actor_handles (handle_lower, channel_id)
    VALUES (lower(NEW.handle), NEW.id)
    ON CONFLICT (channel_id) WHERE channel_id IS NOT NULL
    DO UPDATE SET handle_lower = EXCLUDED.handle_lower;
    RETURN NEW;
END;
$$;

-- channel_handle_aliases — the renamed side keeps working, in the two different
-- senses a name can keep working.
--
-- FOR HUMANS: `/channels/<old>` and GET /api/v1/channels/{old} answer 301 to the
-- new handle until expires_at, one year from the rename; after that the alias
-- stops resolving and the name is free for a future channel (nothing else could
-- claim it in the meantime, because the account that caused the rename holds the
-- reservation).
--
-- FOR PEERS: `is_actor_id` marks the one alias the ActivityPub actor `id` is
-- built from, and expires_at does not apply to it. A rename must NOT change the
-- actor id: peers hold `…/video-channels/ownera` in their follow rows, in their
-- cached actors and in every activity's attributedTo, and an id that changes
-- underneath them is an actor that silently ceases to exist. So a rename moves
-- the HUMAN handle — and with it preferredUsername, the profile URL and the
-- 301 — and freezes the federated identity where it already was. An alias is a
-- courtesy to humans and expires; a federated id is a promise to other servers
-- and does not.
--
-- An alias is NOT a reservation: the account already holds `handle_lower` in
-- actor_handles, and /accounts/<name> and /video-channels/<name> are different
-- URL namespaces, so both can answer at once without ambiguity.
--
-- The frozen identity lives HERE rather than as a channels column on purpose:
-- adding a column to `channels` would turn every channel query whose SELECT
-- lists the table's columns into a bespoke row type, rippling a rename of one
-- federation concept through the whole channel service.
CREATE TABLE channel_handle_aliases (
    handle_lower TEXT        PRIMARY KEY,
    channel_id   UUID        NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    is_actor_id  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX channel_handle_aliases_channel_idx ON channel_handle_aliases (channel_id);
-- At most one frozen federated identity per channel — an actor id is single
-- valued by definition, and a second row would make "which id do we serve?"
-- unanswerable.
CREATE UNIQUE INDEX channel_handle_aliases_actor_id_idx
    ON channel_handle_aliases (channel_id) WHERE is_actor_id;

-- (2) THE BACKFILL, and the rule it applies. Accounts are seeded first and win
-- every collision, because a username is a SIGN-IN identifier: renaming it would
-- lock a person out of their own instance, while renaming a channel handle
-- changes a URL that this migration also keeps alive.
--
-- The whole backfill is a FUNCTION rather than an inline DO block so the rule
-- can be exercised on a seeded collision by a test, instead of only by the one
-- run that ever executes it. It is idempotent: every insert is ON CONFLICT DO
-- NOTHING and the rename loop only ever sees channels that still collide.
CREATE OR REPLACE FUNCTION backfill_actor_handles() RETURNS INT
LANGUAGE plpgsql AS $$
DECLARE
    ch        RECORD;
    candidate TEXT;
    suffix    INT;
    renamed   INT := 0;
BEGIN
    INSERT INTO actor_handles (handle_lower, user_id)
    SELECT lower(username), id FROM users
    ON CONFLICT DO NOTHING;

    -- Rename every channel whose handle collides with an account,
    -- deterministically: `<handle>-channel`, then `-channel-2`, `-channel-3`, …
    -- until the name is free of both namespaces and of the aliases already
    -- written. Each rename writes the alias that redirects humans AND freezes
    -- the ActivityPub identity, plus an audit row — audit_log carries no prose,
    -- so the names go in `reason` as the structured `from=… to=…` pair every
    -- other rename-shaped action here uses, and in `metadata` as fields.
    FOR ch IN
        SELECT c.id, c.handle
        FROM channels c
        JOIN actor_handles a ON a.handle_lower = lower(c.handle)
        WHERE a.user_id IS NOT NULL
        ORDER BY c.created_at, c.id
    LOOP
        candidate := ch.handle || '-channel';
        suffix := 1;
        WHILE EXISTS (SELECT 1 FROM actor_handles WHERE handle_lower = lower(candidate))
           OR EXISTS (SELECT 1 FROM channels WHERE lower(handle) = lower(candidate))
           OR EXISTS (SELECT 1 FROM channel_handle_aliases WHERE handle_lower = lower(candidate))
        LOOP
            suffix := suffix + 1;
            candidate := ch.handle || '-channel-' || suffix::text;
        END LOOP;

        INSERT INTO channel_handle_aliases (handle_lower, channel_id, is_actor_id, expires_at)
        VALUES (lower(ch.handle), ch.id, TRUE, now() + INTERVAL '365 days')
        ON CONFLICT (handle_lower) DO NOTHING;

        UPDATE channels SET handle = candidate WHERE id = ch.id;

        INSERT INTO audit_log (action, result, domain, actor_kind,
                               resource_type, resource_id, reason, metadata)
        VALUES ('content.channel.handle_renamed', 'success', 'content', 'system',
                'channel', ch.id::text,
                'from=' || ch.handle || ' to=' || candidate,
                jsonb_build_object('from', ch.handle, 'to', candidate,
                                   'cause', 'handle_namespace_backfill'));
        renamed := renamed + 1;
    END LOOP;

    -- Everything that survived the rename is a channel handle nothing else holds.
    INSERT INTO actor_handles (handle_lower, channel_id)
    SELECT lower(handle), id FROM channels
    ON CONFLICT DO NOTHING;

    RETURN renamed;
END;
$$;

SELECT backfill_actor_handles();

-- Only now can the triggers arm: attaching them before the backfill would make
-- the backfill's own UPDATEs fight the reservation it is building.
CREATE TRIGGER users_reserve_handle
AFTER INSERT OR UPDATE OF username ON users
FOR EACH ROW EXECUTE FUNCTION reserve_account_handle();

CREATE TRIGGER channels_reserve_handle
AFTER INSERT OR UPDATE OF handle ON channels
FOR EACH ROW EXECUTE FUNCTION reserve_channel_handle();

-- (3) remote_actors.attributed_to — WHICH ACCOUNT OWNS THIS CHANNEL ACTOR.
-- A block is taken against an ACCOUNT ("@name@domain"), and the rehearsal's
-- finding is that the content is attributed to that account's CHANNELS. The
-- Group actor document already carries `attributedTo` (vidra emits it for
-- PeerTube's validator, and PeerTube requires it), so the owner is knowable at
-- resolve time; it was simply never stored. With it stored, one block reaches
-- every actor the account owns.
ALTER TABLE remote_actors ADD COLUMN attributed_to TEXT NOT NULL DEFAULT '';
CREATE INDEX remote_actors_attributed_to_idx
    ON remote_actors (attributed_to) WHERE attributed_to <> '';

-- (4) blocked_remote_actors — the ADMIN half of the same control. An instance
-- block is a sledgehammer in the other direction: an admin who wants one remote
-- person gone for everyone had to defederate that person's whole server. Keyed
-- on the actor URL for the same reasons the per-viewer table is (0138): the
-- actor may never have been cached, and an eviction must not lift the block.
CREATE TABLE blocked_remote_actors (
    remote_actor_url TEXT        PRIMARY KEY,
    blocked_by       UUID        REFERENCES users (id) ON DELETE SET NULL,
    reason           TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX blocked_remote_actors_created_idx ON blocked_remote_actors (created_at DESC);

-- (5) remote_actor_block_reach — ONE definition of "is this actor blocked for
-- this reader", so the ten viewer-facing queries that ask it cannot drift apart.
--
-- It expands each block along the account→channel edge and unions the admin
-- blocks in with a NULL blocker_id, which is what lets one predicate answer both
-- scopes:
--
--     NOT EXISTS (SELECT 1 FROM remote_actor_block_reach rab
--                  WHERE (rab.blocker_id = <viewer> OR rab.blocker_id IS NULL)
--                    AND rab.actor_url = <actor>)
--
-- A NULL viewer (anonymous) makes the per-viewer half trivially false and leaves
-- the admin half intact — an admin block hides for everyone, including readers
-- who are not signed in, which is precisely the difference between the two
-- scopes.
--
-- Both halves are REVERSIBLE by construction: nothing here writes to the content
-- rows, so an unblock restores the hidden rows exactly, which is the same
-- property the local block→remove ruling has.
CREATE VIEW remote_actor_block_reach AS
    SELECT b.blocker_id, b.remote_actor_url AS actor_url
      FROM remote_actor_blocks b
    UNION
    SELECT b.blocker_id, ra.actor_url
      FROM remote_actor_blocks b
      JOIN remote_actors ra ON ra.attributed_to = b.remote_actor_url
    UNION
    SELECT NULL::uuid AS blocker_id, a.remote_actor_url AS actor_url
      FROM blocked_remote_actors a
    UNION
    SELECT NULL::uuid AS blocker_id, ra.actor_url
      FROM blocked_remote_actors a
      JOIN remote_actors ra ON ra.attributed_to = a.remote_actor_url;

-- (6) remote_channel_follows gains 'rejected'. The rehearsal: a Follow refused
-- while an instance block stood left the SENDER's row `pending` forever — the
-- receiver records nothing and sends nothing, and the sender never re-attempts,
-- so a creator's UI shows a follow request that can never resolve. A Reject also
-- used to DELETE the row, which is the same silence by another route: the
-- request simply vanishes from the list.
--
-- 'rejected' is a terminal, creator-VISIBLE state. Re-following the same actor
-- moves it back to 'pending' and sends a fresh Follow — the retry is one
-- deliberate act by the person whose follow it is, not a loop.
ALTER TABLE remote_channel_follows DROP CONSTRAINT remote_channel_follows_state_check;
ALTER TABLE remote_channel_follows
    ADD CONSTRAINT remote_channel_follows_state_check
    CHECK (state IN ('pending', 'accepted', 'rejected'));

-- (7) remote_video_comments.parent_object_url — the mirrored thread stops being
-- flat. The rehearsal measured it: only a Note whose inReplyTo is the VIDEO's
-- object url is mirrored, so every deeper reply is delivered successfully and
-- dropped silently — the same failure shape A29 found, one level down. The
-- parent is stored as the origin's object URL rather than a local FK because the
-- replies can arrive in any order: a reply may reach us before the comment it
-- answers, and a self-referencing FK would refuse it rather than hold it.
ALTER TABLE remote_video_comments ADD COLUMN parent_object_url TEXT NOT NULL DEFAULT '';
CREATE INDEX remote_video_comments_parent_idx
    ON remote_video_comments (remote_video_id, parent_object_url)
    WHERE parent_object_url <> '';
