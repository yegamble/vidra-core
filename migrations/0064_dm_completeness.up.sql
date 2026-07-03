-- 0064: DM completeness (product-decisions.md §14). Rounds out normal (plaintext)
-- direct messaging with attachments, link previews, read receipts, and
-- per-message delete/report. Encrypted conversations are unaffected — their
-- bodies are opaque ciphertext, so attachments/previews/tombstones do not apply
-- (the HTTP layer rejects these operations on an encrypted conversation).

-- Link previews: a best-effort, SSRF-guarded fetch of the FIRST URL in a
-- plaintext message. Keyed by the URL hash so the fetch result is a SHARED cache
-- across every message that references the same URL. state='pending' until the
-- async fetch resolves it to 'ready' (OpenGraph title/description/image parsed)
-- or 'failed' (fetch/parse error, non-html, oversize, or SSRF-blocked). A send
-- is NEVER blocked or failed by preview fetching.
CREATE TABLE link_previews (
    url_hash    TEXT        PRIMARY KEY,               -- sha256 hex of the fetched URL
    url         TEXT        NOT NULL,
    state       TEXT        NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'ready', 'failed')),
    title       TEXT        NOT NULL DEFAULT '',
    description TEXT        NOT NULL DEFAULT '',
    image_url   TEXT        NOT NULL DEFAULT '',
    fetched_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-message additions:
--   deleted_at          — set when the sender tombstones the message (body is
--                         also overwritten with '[deleted]'); NULL means live.
--   link_preview_url_hash — the shared link_previews row this message's first URL
--                         maps to (NULL when the message carried no URL, or the
--                         conversation is encrypted). ON DELETE SET NULL so a
--                         preview-cache purge never removes messages.
ALTER TABLE messages
    ADD COLUMN deleted_at            TIMESTAMPTZ,
    ADD COLUMN link_preview_url_hash TEXT REFERENCES link_previews (url_hash) ON DELETE SET NULL;

-- Read receipts: each participant's watermark — the newest message they have
-- read. The inbox derives unread counts from it; the thread exposes the PEER's
-- watermark (subject to the peer's read-receipts privacy toggle, stored in
-- notification_prefs under the pseudo-type 'read_receipts'). ON DELETE SET NULL
-- so tombstoning/purging a message never corrupts a watermark.
ALTER TABLE conversation_participants
    ADD COLUMN last_read_message_id UUID REFERENCES messages (id) ON DELETE SET NULL;

-- DM attachments (product-decisions.md §14): a participant uploads a bounded
-- (<=25 MiB) image/video/audio/pdf, malware-scanned fail-closed when enabled,
-- stored at dm-attachments/<conversation>/<id>.<ext>. It is created UNLINKED
-- (message_id NULL, owned by the uploader) and linked to a message on send
-- (<=4 own-uploaded, same conversation). Downloads are participant-gated.
CREATE TABLE message_attachments (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    conversation_id UUID        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    uploader_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    message_id      UUID        REFERENCES messages (id) ON DELETE CASCADE, -- NULL until linked on send
    kind            TEXT        NOT NULL CHECK (kind IN ('image', 'video', 'audio', 'pdf')),
    content_type    TEXT        NOT NULL,
    filename        TEXT        NOT NULL,
    size_bytes      BIGINT      NOT NULL CHECK (size_bytes >= 0),
    storage_key     TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Attachments for the messages on a page (message_id = ANY(...)).
CREATE INDEX message_attachments_message_idx ON message_attachments (message_id);
-- The uploader's still-unlinked attachments in a conversation (send validation).
CREATE INDEX message_attachments_uploader_unlinked_idx
    ON message_attachments (conversation_id, uploader_id)
    WHERE message_id IS NULL;

-- Reports gain a 'message' target: either participant may report a DM message.
-- message_body_snapshot preserves the reported text for the moderator even after
-- the sender tombstones the message; message_id is SET NULL if the message row
-- is later hard-deleted (the snapshot survives).
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('video', 'comment', 'account', 'remote_video', 'message'));

ALTER TABLE reports
    ADD COLUMN message_id            UUID REFERENCES messages (id) ON DELETE SET NULL,
    ADD COLUMN message_body_snapshot TEXT NOT NULL DEFAULT '';

-- A user reports any given message at most once.
CREATE UNIQUE INDEX reports_reporter_message_idx
    ON reports (reporter_id, message_id) WHERE message_id IS NOT NULL;
