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
-- Messages in a conversation, newest first, with the sender's username, the
-- tombstone flag, and the joined link preview (only once the async fetch has
-- resolved to 'ready'; pending/failed previews stay hidden). The caller has
-- already verified the requester is a participant and attaches attachments
-- separately.
SELECT m.id, m.conversation_id, m.sender_id, m.body, m.created_at,
       (m.deleted_at IS NOT NULL)::bool AS deleted,
       u.username AS sender_username, u.display_name AS sender_display_name,
       lp.url AS preview_url, lp.title AS preview_title,
       lp.description AS preview_description, lp.image_url AS preview_image
FROM messages m
JOIN users u ON u.id = m.sender_id
LEFT JOIN link_previews lp
       ON lp.url_hash = m.link_preview_url_hash AND lp.state = 'ready'
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
       COALESCE(lm.created_at, c.updated_at) AS last_message_at,
       -- Unread = the peer's messages newer than the caller's read watermark
       -- (all of them when the caller has never read). The caller's own messages
       -- never count as unread.
       (SELECT count(*) FROM messages um
        WHERE um.conversation_id = c.id
          AND um.sender_id <> me.user_id
          AND (me.last_read_message_id IS NULL
               OR um.created_at > (SELECT created_at FROM messages WHERE id = me.last_read_message_id)))::bigint AS unread_count
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

-- ============================================================================
-- DM completeness (product-decisions.md §14): read receipts, attachments,
-- link previews, per-message delete/report support.
-- ============================================================================

-- name: GetMessage :one
-- A single message's core fields, used by the delete/report/read paths to
-- resolve its conversation and current state. deleted_at is non-null once
-- tombstoned.
SELECT id, conversation_id, sender_id, body, created_at, deleted_at
FROM messages WHERE id = $1;

-- name: GetLatestMessageID :one
-- The newest message in a conversation (for "mark all read"). No rows when the
-- conversation has no messages yet.
SELECT id FROM messages
WHERE conversation_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: SetLastReadMessage :exec
-- Advance the caller's read watermark to message_id (idempotent, advance-only:
-- an older or equal watermark is never moved backwards). The message must be in
-- the conversation (enforced by the caller).
UPDATE conversation_participants cp
SET last_read_message_id = sqlc.arg('message_id')
WHERE cp.conversation_id = sqlc.arg('conversation_id')
  AND cp.user_id = sqlc.arg('user_id')
  AND (
      cp.last_read_message_id IS NULL
      OR (SELECT nm.created_at FROM messages nm WHERE nm.id = sqlc.arg('message_id'))
         >= (SELECT om.created_at FROM messages om WHERE om.id = cp.last_read_message_id)
  );

-- name: GetPeerReadWatermark :one
-- The OTHER participant's read watermark (the message id they have read up to),
-- for the thread's "seen" indicator. Nullable; the caller hides it when the peer
-- disabled read receipts.
SELECT last_read_message_id
FROM conversation_participants
WHERE conversation_id = sqlc.arg('conversation_id') AND user_id <> sqlc.arg('user_id')
LIMIT 1;

-- name: TombstoneMessage :execrows
-- Sender-only soft delete: overwrite the body with the tombstone and mark
-- deleted_at, dropping any link preview. Returns rows affected (1 = tombstoned
-- now, 0 = not the sender / already deleted / unknown).
UPDATE messages
SET body = '[deleted]', deleted_at = now(), link_preview_url_hash = NULL
WHERE id = sqlc.arg('id') AND sender_id = sqlc.arg('sender_id') AND deleted_at IS NULL;

-- name: CreateMessageAttachment :one
-- Store an UNLINKED attachment (message_id NULL) owned by the uploader. The id
-- is generated by the caller so it can build the storage key first.
INSERT INTO message_attachments
    (id, conversation_id, uploader_id, kind, content_type, filename, size_bytes, storage_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, conversation_id, uploader_id, message_id, kind, content_type, filename, size_bytes, storage_key, created_at;

-- name: GetMessageAttachment :one
-- One attachment by id (participant gating is enforced by the caller against the
-- returned conversation_id).
SELECT id, conversation_id, uploader_id, message_id, kind, content_type, filename, size_bytes, storage_key, created_at
FROM message_attachments WHERE id = $1;

-- name: ListOwnedUnlinkedAttachments :many
-- The caller's still-unlinked attachments in a conversation, by id — the
-- send-time validation that every referenced attachment is own-uploaded, in the
-- same conversation, and not already attached to a message.
SELECT id, storage_key
FROM message_attachments
WHERE id = ANY(sqlc.arg('ids')::uuid[])
  AND uploader_id = sqlc.arg('uploader_id')
  AND conversation_id = sqlc.arg('conversation_id')
  AND message_id IS NULL;

-- name: LinkAttachmentsToMessage :exec
-- Attach the validated (own-uploaded, same-conversation, unlinked) attachments
-- to the newly created message.
UPDATE message_attachments
SET message_id = sqlc.arg('message_id')
WHERE id = ANY(sqlc.arg('ids')::uuid[])
  AND uploader_id = sqlc.arg('uploader_id')
  AND conversation_id = sqlc.arg('conversation_id')
  AND message_id IS NULL;

-- name: ListAttachmentsForMessages :many
-- Attachments for the messages on a page (message_id = ANY). Grouped by the
-- caller into each message view.
SELECT id, message_id, kind, content_type, filename, size_bytes
FROM message_attachments
WHERE message_id = ANY(sqlc.arg('message_ids')::uuid[])
ORDER BY created_at ASC, id ASC;

-- name: ListAttachmentKeysByMessage :many
-- Storage keys + ids of a message's attachments, for tombstone cleanup.
SELECT id, storage_key FROM message_attachments WHERE message_id = $1;

-- name: DeleteAttachmentsByMessage :exec
DELETE FROM message_attachments WHERE message_id = $1;

-- name: UpsertPendingLinkPreview :exec
-- Insert a shared, pending link-preview cache row for a URL (idempotent — a
-- concurrent send for the same URL reuses the existing row).
INSERT INTO link_previews (url_hash, url) VALUES ($1, $2)
ON CONFLICT (url_hash) DO NOTHING;

-- name: SetMessageLinkPreview :exec
UPDATE messages SET link_preview_url_hash = sqlc.arg('url_hash') WHERE id = sqlc.arg('id');

-- name: GetLinkPreview :one
SELECT url_hash, url, state, title, description, image_url, fetched_at, created_at
FROM link_previews WHERE url_hash = $1;

-- name: ResolveLinkPreview :exec
-- Record the outcome of a preview fetch on the shared cache row: 'ready' with
-- the parsed OpenGraph fields, or 'failed'. Never touches messages.
UPDATE link_previews
SET state = sqlc.arg('state'),
    title = sqlc.arg('title'),
    description = sqlc.arg('description'),
    image_url = sqlc.arg('image_url'),
    fetched_at = now()
WHERE url_hash = sqlc.arg('url_hash');
