-- 0070: DM attachment "doc" kind (messaging-v2.md D6; product-decisions.md §14
-- amendment; DECIDED 2026-07-07). Vidra enforces Facebook-Messenger-parity DM
-- attachment limits instead of counting DM attachments against the storage quota.
-- Office documents (PDF was already allowed; DOC/DOCX/PPT/PPTX/XLS/XLSX added) are
-- accepted as a new coarse kind 'doc'. Only the kind CHECK constraint changes here —
-- content_type, storage layout, and fail-closed malware scanning are unchanged. The
-- per-file size cap (now 100 MiB = 104,857,600 bytes) and per-message attachment
-- count (30) live in application config, not the schema.
ALTER TABLE message_attachments DROP CONSTRAINT message_attachments_kind_check;
ALTER TABLE message_attachments ADD CONSTRAINT message_attachments_kind_check
    CHECK (kind IN ('image', 'video', 'audio', 'pdf', 'doc'));
