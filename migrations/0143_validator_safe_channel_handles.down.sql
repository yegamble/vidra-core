-- Reverse 0143 and NOTHING else: the interim `-channel` names come back, the
-- aliases this migration minted go away, and 0142's own rule is restored as the
-- backfill function. What 0142 did — the reservation, the original alias, the
-- frozen ActivityPub id — is 0142's to undo and is not touched here.
--
-- The order matters. The alias for the interim name is deleted BEFORE the
-- channel takes that name back, because the mint checks
-- channel_handle_aliases and the rename trigger moves the reservation onto
-- whatever handle the row ends up with; leaving the alias in place would mean a
-- channel and an alias claiming the same string.
DO $$
DECLARE
    ch RECORD;
BEGIN
    FOR ch IN
        SELECT c.id, a.handle_lower AS interim_handle
        FROM channels c
        JOIN channel_handle_aliases a ON a.channel_id = c.id AND NOT a.is_actor_id
        WHERE a.handle_lower ~ '-channel(-[0-9]+)?$'
    LOOP
        DELETE FROM channel_handle_aliases WHERE handle_lower = ch.interim_handle;
        UPDATE channels SET handle = ch.interim_handle WHERE id = ch.id;
    END LOOP;
END;
$$;

-- The ledger must not describe a rename that has been reversed. These rows are
-- identified exactly — one cause, written by one function — so nothing a human
-- or another subsystem recorded is at risk.
DELETE FROM audit_log
WHERE action = 'content.channel.handle_renamed'
  AND metadata ->> 'cause' = 'handle_validator_alphabet';

DROP FUNCTION IF EXISTS rename_channel_handles_to_validator_alphabet();

-- 0142's backfill, verbatim, so a database rolled back to 141 and migrated
-- forward again reproduces 0142's own behaviour rather than 0143's.
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

    INSERT INTO actor_handles (handle_lower, channel_id)
    SELECT lower(handle), id FROM channels
    ON CONFLICT DO NOTHING;

    RETURN renamed;
END;
$$;

DROP FUNCTION IF EXISTS mint_channel_handle(TEXT);
