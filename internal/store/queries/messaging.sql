-- name: CreateConversation :one
-- Create a 1:1 conversation for the given dm_key. On conflict (it already
-- exists) returns no row, so the caller falls back to GetConversationByDMKey.
INSERT INTO conversations (dm_key)
VALUES ($1)
ON CONFLICT (dm_key) DO NOTHING
RETURNING id, created_at, updated_at;

-- name: GetConversationByDMKey :one
SELECT id, created_at, updated_at FROM conversations WHERE dm_key = $1;

-- name: AddConversationParticipant :exec
-- Add a participant (idempotent). A non-existent user raises a foreign-key
-- violation, which the service maps to "recipient not found".
INSERT INTO conversation_participants (conversation_id, user_id)
VALUES ($1, $2)
ON CONFLICT (conversation_id, user_id) DO NOTHING;

-- name: IsConversationParticipant :one
SELECT EXISTS (
    SELECT 1 FROM conversation_participants
    WHERE conversation_id = $1 AND user_id = $2
);

-- name: GetOtherParticipant :one
-- The other member of a 1:1 conversation (whoever isn't the given user). Used to
-- address a "you have a new message" notification to the recipient.
SELECT user_id FROM conversation_participants
WHERE conversation_id = $1 AND user_id <> $2
LIMIT 1;

-- name: CreateMessage :one
INSERT INTO messages (conversation_id, sender_id, body)
VALUES ($1, $2, $3)
RETURNING id, conversation_id, sender_id, body, created_at;

-- name: TouchConversation :exec
-- Bump updated_at so the conversation sorts to the top of the sender's list.
UPDATE conversations SET updated_at = now() WHERE id = $1;

-- name: ListMessages :many
-- Messages in a conversation, newest first, with the sender's username. The
-- caller has already verified the requester is a participant.
SELECT m.id, m.conversation_id, m.sender_id, m.body, m.created_at,
       u.username AS sender_username, u.display_name AS sender_display_name
FROM messages m
JOIN users u ON u.id = m.sender_id
WHERE m.conversation_id = sqlc.arg('conversation_id')
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: ListConversations :many
-- The caller's 1:1 conversations, most-recently-active first, with the other
-- participant's identity and the last message preview. Encrypted conversations
-- appear too (flagged); their preview is empty — envelopes are opaque.
SELECT c.id, c.updated_at, c.encrypted,
       other.user_id AS other_user_id,
       ou.username AS other_username,
       ou.display_name AS other_display_name,
       -- COALESCE keeps these non-null for a conversation with no messages yet
       -- (an empty body reliably signals "no messages"; message bodies are never
       -- empty). This also matches sqlc's non-null inference for the LATERAL join.
       COALESCE(lm.body, '') AS last_message_body,
       COALESCE(lm.created_at, c.updated_at) AS last_message_at
FROM conversations c
JOIN conversation_participants me ON me.conversation_id = c.id AND me.user_id = sqlc.arg('user_id')
JOIN conversation_participants other ON other.conversation_id = c.id AND other.user_id <> sqlc.arg('user_id')
JOIN users ou ON ou.id = other.user_id
LEFT JOIN LATERAL (
    SELECT body, created_at FROM messages m
    WHERE m.conversation_id = c.id
    ORDER BY m.created_at DESC LIMIT 1
) lm ON TRUE
ORDER BY c.updated_at DESC, c.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
