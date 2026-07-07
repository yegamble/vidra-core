-- 0070 down: revert the 'doc' attachment kind to the original four kinds. Any
-- existing rows with kind='doc' would violate the narrower CHECK, so a rollback on
-- a database that has already accepted office-document attachments must remove or
-- reclassify those rows first (standard down-migration caveat).
ALTER TABLE message_attachments DROP CONSTRAINT message_attachments_kind_check;
ALTER TABLE message_attachments ADD CONSTRAINT message_attachments_kind_check
    CHECK (kind IN ('image', 'video', 'audio', 'pdf'));
