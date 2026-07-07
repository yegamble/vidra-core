-- 0068 down: drop the DM attachment intrinsic-dimension columns.
ALTER TABLE message_attachments
    DROP COLUMN IF EXISTS width,
    DROP COLUMN IF EXISTS height;
