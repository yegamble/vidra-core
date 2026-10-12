-- name: GetReportReporterID :one
-- A report's LOCAL reporter, for the message-thread access check (0163): NULL
-- for a remote Flag report (0153), which has no local reporter to write to. An
-- unknown id yields no row, which the service maps to "not found".
SELECT reporter_id FROM reports WHERE id = $1;

-- name: CreateReportMessage :one
-- Append a message to a report's thread. is_moderator is decided by the caller
-- (the author is not the report's reporter) and frozen here, so a later role
-- change never rewrites which side of the thread said what.
INSERT INTO report_messages (report_id, author_id, is_moderator, body)
VALUES (sqlc.arg('report_id'), sqlc.arg('author_id')::uuid, sqlc.arg('is_moderator'), sqlc.arg('body'))
RETURNING id, created_at;

-- name: ListReportMessages :many
-- A report's thread, oldest first, with each author's username ('' once the
-- author's account was deleted — author_id is then NULL too).
SELECT m.id, m.author_id, m.is_moderator, m.body, m.created_at,
       COALESCE(u.username, '')::text AS author_username
FROM report_messages m
LEFT JOIN users u ON u.id = m.author_id
WHERE m.report_id = sqlc.arg('report_id')
ORDER BY m.created_at, m.id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountReportMessages :one
-- How many rows ListReportMessages would return, ignoring pagination.
SELECT count(*)::bigint FROM report_messages WHERE report_id = $1;
