-- 0069: keyset-pagination index tuning (messaging-v2.md D2). Thread history now
-- pages by a row-value keyset cursor: WHERE (created_at, id) < (cursor)
-- ORDER BY created_at DESC, id DESC. Extending the existing ordering indexes with
-- a trailing `id DESC` lets Postgres satisfy that comparison + ordering as a pure
-- index scan (no in-memory sort), for both plaintext messages and encrypted
-- envelopes. Additive: the previous prefix (conversation_id, created_at DESC) is
-- still usable by the offset-paged queries.
DROP INDEX IF EXISTS messages_conversation_created_idx;
CREATE INDEX messages_conversation_created_idx
    ON messages (conversation_id, created_at DESC, id DESC);

DROP INDEX IF EXISTS e2ee_messages_recipient_idx;
CREATE INDEX e2ee_messages_recipient_idx
    ON e2ee_messages (conversation_id, recipient_device_id, created_at DESC, id DESC);
