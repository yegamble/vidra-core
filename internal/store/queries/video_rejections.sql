-- name: RecordVideoRejection :exec
-- Store the moderator's rejection note for a video (migration 0130). Idempotent
-- on the video: a second rejection would replace the note and the acting
-- moderator, matching BlockVideo's re-block semantics. The note may be empty —
-- the row still records who rejected the upload and when.
INSERT INTO video_rejections (video_id, note, rejected_by)
VALUES ($1, $2, $3)
ON CONFLICT (video_id) DO UPDATE
    SET note = EXCLUDED.note, rejected_by = EXCLUDED.rejected_by, created_at = now();

-- name: GetVideoRejectionNote :one
-- The stored rejection note for one video, or '' when it was never rejected.
SELECT COALESCE((SELECT note FROM video_rejections WHERE video_id = $1), '')::text AS note;
