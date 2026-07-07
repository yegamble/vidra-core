-- 0068: DM attachment intrinsic dimensions (messaging-v2.md D1). Probed at upload
-- for images only (image.DecodeConfig, config-only decode). NULL means unknown:
-- pre-migration rows, non-image kinds, or a probe failure (non-fatal — the upload
-- still succeeds). Lets clients reserve intrinsic-ratio space and avoid layout
-- shift. Both dimensions are bounded > 0 when present (absurd sizes are rejected
-- at upload with a 422 before the row is written).
ALTER TABLE message_attachments
    ADD COLUMN width  INTEGER CHECK (width  IS NULL OR width  > 0),
    ADD COLUMN height INTEGER CHECK (height IS NULL OR height > 0);
