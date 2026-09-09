-- 0143: the handle a rename MINTS and the handle POST /channels ACCEPTS are the
-- same shape (A29 follow-ups).
--
-- WHAT THE REHEARSAL MEASURED. 0142's backfill renames a channel that collides
-- with an account to `<handle>-channel`, then `-channel-2`, `-channel-3`, … and
-- the lab watched it produce `creatora-channel-2`. The instance's own channel
-- validator refuses that name: `POST /channels` answers 422 "must be 3–30
-- chars: letters, digits, or underscore" for anything containing a hyphen.
-- Nothing was broken on the day — reads are unvalidated, so the renamed channel
-- answered 200 at its new handle and federated normally — but the two rules
-- disagreed about what a handle IS, which costs the operator the two things a
-- rename is supposed to leave intact: they cannot re-create the name the
-- migration gave them, and they cannot type it into any form that validates.
-- The owner's ruling is that the rename uses the validator's alphabet.
--
-- (1) mint_channel_handle — ONE place that decides what a renamed channel is
-- called, so the rule cannot be stated twice and drift once. It is a function
-- rather than inline SQL for the same reason 0142's backfill is: a rule only a
-- one-shot migration executes is a rule no test can exercise.
--
-- The alphabet is the validator's, in both directions:
--
--   * every character outside [A-Za-z0-9_] in the SOURCE handle folds to an
--     underscore. The base is a handle this database already holds, and rows
--     predating the current validator (or written by an importer) may carry
--     anything; folding means the minted name passes whatever the source was.
--   * the whole name is kept inside the validator's 30-character ceiling by
--     truncating the ROOT, not the suffix — a name that ended up as
--     `<31 chars>_channel` would be refused exactly as a hyphen is, and
--     truncating the suffix instead would collapse `_channel2` back onto
--     `_channel` and lose the loop's whole purpose.
--   * the minimum length takes care of itself: `_channel` is 8 characters, so
--     even an empty root clears the 3-character floor.
--
-- The suffix is `_channel`, `_channel2`, `_channel3`, … with NO separator
-- before the number, because `_channel_2` would read as a different channel
-- rather than as the second candidate for one name.
CREATE OR REPLACE FUNCTION mint_channel_handle(base TEXT) RETURNS TEXT
LANGUAGE plpgsql AS $$
DECLARE
    root      TEXT;
    suffix    TEXT := '';
    n         INT  := 1;
    candidate TEXT;
BEGIN
    root := regexp_replace(COALESCE(base, ''), '[^A-Za-z0-9_]', '_', 'g');
    LOOP
        candidate := left(root, 30 - length('_channel' || suffix)) || '_channel' || suffix;
        EXIT WHEN NOT EXISTS (SELECT 1 FROM actor_handles          WHERE handle_lower = lower(candidate))
              AND NOT EXISTS (SELECT 1 FROM channels               WHERE lower(handle) = lower(candidate))
              AND NOT EXISTS (SELECT 1 FROM channel_handle_aliases WHERE handle_lower = lower(candidate));
        n      := n + 1;
        suffix := n::text;
    END LOOP;
    RETURN candidate;
END;
$$;

-- (2) The backfill now mints through it. Everything else about the rule is
-- unchanged and deliberately so: accounts are still seeded first and still win
-- every collision (a username is a sign-in identifier), the rename still writes
-- the alias that redirects humans AND freezes the ActivityPub identity, and the
-- whole function is still idempotent.
CREATE OR REPLACE FUNCTION backfill_actor_handles() RETURNS INT
LANGUAGE plpgsql AS $$
DECLARE
    ch        RECORD;
    candidate TEXT;
    renamed   INT := 0;
BEGIN
    INSERT INTO actor_handles (handle_lower, user_id)
    SELECT lower(username), id FROM users
    ON CONFLICT DO NOTHING;

    FOR ch IN
        SELECT c.id, c.handle
        FROM channels c
        JOIN actor_handles a ON a.handle_lower = lower(c.handle)
        WHERE a.user_id IS NOT NULL
        ORDER BY c.created_at, c.id
    LOOP
        candidate := mint_channel_handle(ch.handle);

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

    INSERT INTO actor_handles (handle_lower, channel_id)
    SELECT lower(handle), id FROM channels
    ON CONFLICT DO NOTHING;

    RETURN renamed;
END;
$$;

-- (3) THE SECOND RENAME. An instance that already ran 0142 is carrying names
-- the new rule would never have minted, and leaving them there would mean the
-- fix only reaches instances that had not upgraded yet — i.e. nobody who hit
-- the bug. So the migration re-runs the rule for exactly those rows.
--
-- WHICH ROWS. A channel is 0142's work if it holds an alias marked
-- `is_actor_id` (the backfill is the only thing that mints one) AND its CURRENT
-- handle still has the `-channel` shape that backfill produced. Both clauses
-- matter: the first keeps a channel a human deliberately called `my-channel`
-- out of this, and the second keeps the migration off channels a later,
-- validator-clean rename already fixed.
--
-- WHAT A SECOND RENAME COSTS, AND WHAT IT MUST NOT MOVE:
--
--   * the ACTIVITYPUB ID DOES NOT MOVE. 0142 froze it on the ORIGINAL colliding
--     name and peers hold that id in their follow rows, their cached actors and
--     every activity's attributedTo. This migration does not touch the
--     `is_actor_id` alias, so a peer that has been talking to
--     `…/video-channels/creatora` since before either rename keeps talking to
--     it, and never learns that the human-facing handle changed twice.
--   * the `-channel` NAME KEEPS RESOLVING, as a second, ordinary alias: 301 to
--     the new handle for a year, exactly the courtesy 0142 extended to the
--     original name. Anyone who bookmarked or published the interim name in the
--     window between the two upgrades is not sent to a 404.
--   * the rename is AUDITED AGAIN, with its own cause, because a ledger that
--     recorded the first rename and not the second would describe a channel
--     under a name it no longer has.
CREATE OR REPLACE FUNCTION rename_channel_handles_to_validator_alphabet() RETURNS INT
LANGUAGE plpgsql AS $$
DECLARE
    ch        RECORD;
    candidate TEXT;
    renamed   INT := 0;
BEGIN
    FOR ch IN
        SELECT c.id, c.handle AS interim_handle, a.handle_lower AS original_handle
        FROM channels c
        JOIN channel_handle_aliases a ON a.channel_id = c.id AND a.is_actor_id
        WHERE c.handle ~ '-channel(-[0-9]+)?$'
        ORDER BY c.created_at, c.id
    LOOP
        candidate := mint_channel_handle(ch.original_handle);

        INSERT INTO channel_handle_aliases (handle_lower, channel_id, is_actor_id, expires_at)
        VALUES (lower(ch.interim_handle), ch.id, FALSE, now() + INTERVAL '365 days')
        ON CONFLICT (handle_lower) DO NOTHING;

        UPDATE channels SET handle = candidate WHERE id = ch.id;

        INSERT INTO audit_log (action, result, domain, actor_kind,
                               resource_type, resource_id, reason, metadata)
        VALUES ('content.channel.handle_renamed', 'success', 'content', 'system',
                'channel', ch.id::text,
                'from=' || ch.interim_handle || ' to=' || candidate,
                jsonb_build_object('from', ch.interim_handle, 'to', candidate,
                                   'original', ch.original_handle,
                                   'cause', 'handle_validator_alphabet'));
        renamed := renamed + 1;
    END LOOP;
    RETURN renamed;
END;
$$;

SELECT rename_channel_handles_to_validator_alphabet();
