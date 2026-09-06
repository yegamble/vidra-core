-- E2EE messaging (fix_plan P11.2, .ralph/specs/e2ee.md). The server stores only
-- public key material and opaque ciphertext — no plaintext, no private keys.

-- name: CreateE2EEConversation :one
-- Create an encrypted 1:1 conversation for the given "e2ee:"-prefixed dm_key.
-- On conflict (it already exists) returns no row, so the caller falls back to
-- GetE2EEConversationByDMKey. encrypted is immutable: no update path exists.
INSERT INTO conversations (dm_key, encrypted)
VALUES ($1, TRUE)
ON CONFLICT (dm_key) DO NOTHING
RETURNING id, created_at, updated_at;

-- name: GetE2EEConversationByDMKey :one
SELECT id, created_at, updated_at FROM conversations
WHERE dm_key = $1 AND encrypted;

-- name: GetConversationForParticipant :one
-- The conversation's encrypted flag, visible only to a participant (no row for
-- outsiders/unknown ids, so existence is not leaked).
SELECT c.encrypted
FROM conversations c
JOIN conversation_participants p ON p.conversation_id = c.id AND p.user_id = sqlc.arg('user_id')
WHERE c.id = sqlc.arg('conversation_id');

-- name: UsersShareConversation :one
-- Participant gate for the peer key-directory endpoints: do the two users share
-- at least one conversation (plaintext or encrypted)?
SELECT EXISTS (
    SELECT 1
    FROM conversation_participants a
    JOIN conversation_participants b ON b.conversation_id = a.conversation_id
    WHERE a.user_id = sqlc.arg('user_a') AND b.user_id = sqlc.arg('user_b')
);

-- name: CreateE2EEDevice :one
INSERT INTO e2ee_devices (user_id, device_name, identity_key, signing_key)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, device_name, identity_key, signing_key, created_at, last_seen_at;

-- name: GetE2EEDevice :one
SELECT id, user_id, device_name, identity_key, signing_key, created_at, last_seen_at
FROM e2ee_devices WHERE id = $1;

-- name: ListE2EEDevicesByUser :many
SELECT id, user_id, device_name, identity_key, signing_key, created_at, last_seen_at
FROM e2ee_devices
WHERE user_id = $1
ORDER BY created_at, id;

-- name: CountE2EEDevicesByUser :one
SELECT COUNT(*) FROM e2ee_devices WHERE user_id = $1;

-- name: DeleteE2EEDevice :execrows
-- Owner-scoped delete; 0 rows means unknown-or-not-owned (the service maps
-- that to "device not found" so ownership is not leaked).
DELETE FROM e2ee_devices WHERE id = $1 AND user_id = $2;

-- name: DeleteE2EEDevicesByUser :exec
-- Account-delete purge (§1): every encrypted-messaging device the account
-- registered, and — through e2ee_one_time_keys.device_id ON DELETE CASCADE —
-- their unclaimed one-time keys. Needed explicitly because the §1 delete
-- ANONYMISES the users row instead of removing it, so the ON DELETE CASCADE on
-- e2ee_devices.user_id never fires. A device row carries a user-chosen
-- device_name, which the tombstone exists to take away.
DELETE FROM e2ee_devices WHERE user_id = $1;

-- name: TouchE2EEDevice :exec
UPDATE e2ee_devices SET last_seen_at = now() WHERE id = $1;

-- name: ListE2EEDevicesForConversation :many
-- Every device of every participant of a conversation. Used to validate the
-- sender device and the per-envelope recipient devices of an encrypted send.
SELECT d.id, d.user_id
FROM e2ee_devices d
JOIN conversation_participants p ON p.user_id = d.user_id
WHERE p.conversation_id = $1;

-- name: InsertE2EEOneTimeKey :exec
-- Idempotent per (device_id, key_id): replenishing the same batch is a no-op.
INSERT INTO e2ee_one_time_keys (device_id, key_id, key)
VALUES ($1, $2, $3)
ON CONFLICT (device_id, key_id) DO NOTHING;

-- name: CountUnclaimedE2EEOneTimeKeys :one
SELECT COUNT(*) FROM e2ee_one_time_keys
WHERE device_id = $1 AND claimed_at IS NULL;

-- name: ClaimE2EEOneTimeKey :one
-- Atomically claim (single-use) the oldest unclaimed one-time key of a device.
-- FOR UPDATE SKIP LOCKED guarantees two concurrent claimers never receive the
-- same key; no row (all claimed) surfaces as pgx.ErrNoRows.
UPDATE e2ee_one_time_keys
SET claimed_at = now()
WHERE id = (
    SELECT k.id FROM e2ee_one_time_keys k
    WHERE k.device_id = $1 AND k.claimed_at IS NULL
    ORDER BY k.created_at, k.id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, device_id, key_id, key;

-- name: CreateE2EEMessage :one
-- One envelope row per recipient device (the client fans out). ciphertext is
-- opaque; the server never inspects it.
INSERT INTO e2ee_messages (conversation_id, sender_device_id, recipient_device_id, message_type, ciphertext, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, conversation_id, sender_device_id, recipient_device_id, message_type, ciphertext, created_at, expires_at;

-- name: ListE2EEMessagesForRecipient :many
-- Envelopes in a conversation addressed to any of the CALLER's devices, newest
-- first. Expired disappearing messages are filtered here too, so expiry is
-- correct between sweeper runs. sender_user_id lets the client attribute the
-- envelope without a second lookup.
SELECT m.id, m.conversation_id, m.sender_device_id, sd.user_id AS sender_user_id,
       m.recipient_device_id, m.message_type, m.ciphertext, m.created_at, m.expires_at
FROM e2ee_messages m
JOIN e2ee_devices rd ON rd.id = m.recipient_device_id AND rd.user_id = sqlc.arg('user_id')
JOIN e2ee_devices sd ON sd.id = m.sender_device_id
WHERE m.conversation_id = sqlc.arg('conversation_id')
  AND (m.expires_at IS NULL OR m.expires_at > now())
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: GetE2EEMessageCursor :one
-- The created_at of one of the CALLER's received envelopes in a conversation, for
-- keyset-pagination cursor validation. No row = the cursor id is unknown, not in
-- this conversation, or not addressed to one of the caller's devices — the HTTP
-- layer maps that to an invalid-cursor 422.
SELECT m.created_at
FROM e2ee_messages m
JOIN e2ee_devices rd ON rd.id = m.recipient_device_id AND rd.user_id = sqlc.arg('user_id')
WHERE m.id = sqlc.arg('id') AND m.conversation_id = sqlc.arg('conversation_id');

-- name: ListE2EEMessagesForRecipientBefore :many
-- Keyset page of a conversation's envelopes addressed to the CALLER's devices,
-- strictly OLDER than the cursor (before_created_at, before_id), newest first,
-- with expired disappearing messages filtered. Mirrors
-- ListE2EEMessagesForRecipient so encrypted threads page identically to plaintext
-- ones; the caller validates the cursor (GetE2EEMessageCursor) first.
SELECT m.id, m.conversation_id, m.sender_device_id, sd.user_id AS sender_user_id,
       m.recipient_device_id, m.message_type, m.ciphertext, m.created_at, m.expires_at
FROM e2ee_messages m
JOIN e2ee_devices rd ON rd.id = m.recipient_device_id AND rd.user_id = sqlc.arg('user_id')
JOIN e2ee_devices sd ON sd.id = m.sender_device_id
WHERE m.conversation_id = sqlc.arg('conversation_id')
  AND (m.expires_at IS NULL OR m.expires_at > now())
  AND (m.created_at, m.id) < (sqlc.arg('before_created_at')::timestamptz, sqlc.arg('before_id')::uuid)
ORDER BY m.created_at DESC, m.id DESC
LIMIT sqlc.arg('result_limit');

-- name: SweepExpiredE2EEMessages :execrows
-- Hard-delete up to limit expired disappearing messages (sweeper worker).
DELETE FROM e2ee_messages
WHERE id IN (
    SELECT id FROM e2ee_messages
    WHERE expires_at IS NOT NULL AND expires_at <= now()
    LIMIT sqlc.arg('result_limit')
);
