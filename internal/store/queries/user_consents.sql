-- name: ListUserConsents :many
-- Everything an account has accepted (0189), oldest first: the documents, the
-- operator's version labels and URLs as they were at the time, how each
-- acceptance arrived, and when. The account export carries it.
SELECT document, version, url, minimum_age, source, accepted_at
FROM user_consents
WHERE user_id = $1
ORDER BY accepted_at, document;
