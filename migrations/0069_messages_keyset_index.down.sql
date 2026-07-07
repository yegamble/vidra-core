-- 0069 down: restore the pre-keyset ordering indexes (drop the trailing id DESC).
DROP INDEX IF EXISTS messages_conversation_created_idx;
CREATE INDEX messages_conversation_created_idx
    ON messages (conversation_id, created_at DESC);

DROP INDEX IF EXISTS e2ee_messages_recipient_idx;
CREATE INDEX e2ee_messages_recipient_idx
    ON e2ee_messages (conversation_id, recipient_device_id, created_at DESC);
