-- Blob-reference consistency queries (phase-2 storage, work item 7). The
-- `verify-blobs` subcommand asks the database what objects it believes are in
-- the store and then asks the store, so these return the OTHER storage_key
-- columns -- the ones media_gc.sql deliberately does not enumerate.
--
-- media_gc.sql covers the prefixes the GC sweep is allowed to delete from
-- (video_files, captions, playlist covers, the HLS trees). Everything below is
-- a reference the sweep never touches because another lifecycle owns it:
-- avatars and banners, account export archives, DM attachments, the instance's
-- own images. A consistency check has the opposite bias -- a dangling reference
-- matters wherever it is -- so it reads both sets.
--
-- Every one of these filters out the empty key rather than leaving it to the
-- caller: '' is not an object, it is a row whose blob has not been written yet
-- (account_exports defaults to it while the job is pending), and asking a
-- bucket whether it holds "" is a question with no useful answer.

-- name: ListAllUserImageKeys :many
-- Avatars and banners for user accounts (migration 0040).
SELECT storage_key FROM user_images WHERE storage_key <> '';

-- name: ListAllChannelImageKeys :many
-- Avatars and banners for channels (migration 0040).
SELECT storage_key FROM channel_images WHERE storage_key <> '';

-- name: ListAllInstanceImageKeys :many
-- The instance's own avatar/banner and the four typed logo slots (0086).
SELECT storage_key FROM instance_images WHERE storage_key <> '';

-- name: ListAllAccountExportKeys :many
-- Completed account-export archives (0057). The column DEFAULTs to '' and stays
-- empty for the whole pending/running life of the job, so the filter above is
-- load-bearing here rather than defensive.
SELECT storage_key FROM account_exports WHERE storage_key <> '';

-- name: ListAllMessageAttachmentKeys :many
-- Direct-message attachments (0064), including the ones still unlinked to a
-- message: an uploaded-but-unsent attachment is a real object with a real row.
SELECT storage_key FROM message_attachments WHERE storage_key <> '';

-- name: ListAllVideoFileHashes :many
-- Every stored video file with the digest recorded for it (0106), so the
-- verifier can re-hash what it downloads and tell three states apart: '' (never
-- computed -- nothing to compare against), 'missing' (the backfill already
-- found no object behind this row), and 64 hex characters (a real digest a
-- re-read must reproduce).
SELECT storage_key, sha256 FROM video_files WHERE storage_key <> '';
