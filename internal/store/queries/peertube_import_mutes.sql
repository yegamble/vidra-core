-- name: ImportAccountMute :execrows
INSERT INTO muted_accounts (muter_id, muted_id, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (muter_id, muted_id) DO NOTHING;

-- name: ImportInstanceMute :execrows
INSERT INTO muted_instances (muter_id, domain, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (muter_id, domain) DO NOTHING;
