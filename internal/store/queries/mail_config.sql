-- name: GetMailConfig :one
-- The single outbound-mail transport document (migration 0151), or no rows when
-- this instance has none and the environment configuration applies. Read at boot
-- and again on every settings-version bump, so it is a primary-key lookup by
-- design.
SELECT id, transport, from_address, from_name, reply_to, settings, secret, updated_by, updated_at
FROM mail_config
WHERE id;

-- name: UpsertMailConfig :one
-- Write the whole document. It is an upsert rather than an UPDATE because the
-- configuration is saved WHOLE or not at all: only the active transport's
-- settings and its one sealed credential are stored, so a partial write would
-- leave the row describing a transport with another transport's fields.
--
-- The secret is passed already sealed; this layer never sees plaintext.
INSERT INTO mail_config (id, transport, from_address, from_name, reply_to, settings, secret, updated_by, updated_at)
VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (id) DO UPDATE
SET transport    = EXCLUDED.transport,
    from_address = EXCLUDED.from_address,
    from_name    = EXCLUDED.from_name,
    reply_to     = EXCLUDED.reply_to,
    settings     = EXCLUDED.settings,
    secret       = EXCLUDED.secret,
    updated_by   = EXCLUDED.updated_by,
    updated_at   = now()
RETURNING id, transport, from_address, from_name, reply_to, settings, secret, updated_by, updated_at;

-- name: DeleteMailConfig :execrows
-- Drop the document so the instance reverts to its ENVIRONMENT mail
-- configuration (or to none). Returns the number of rows removed, so a reset on
-- an instance that never had a document is distinguishable from one that did.
DELETE FROM mail_config WHERE id;
