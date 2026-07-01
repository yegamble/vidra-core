-- 0032: Let a notification point at a conversation, so a "message" notification
-- (a recipient was sent a direct message) can link to the thread. Nullable and
-- type-dependent like the other context columns; cascades if the conversation is
-- deleted. The actor_id column already carries the sender's identity.
ALTER TABLE notifications
    ADD COLUMN conversation_id UUID REFERENCES conversations (id) ON DELETE CASCADE;
