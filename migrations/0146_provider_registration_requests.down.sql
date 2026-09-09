DROP INDEX IF EXISTS registration_requests_pending_provider_idx;
ALTER TABLE registration_requests
    DROP COLUMN IF EXISTS oauth_provider,
    DROP COLUMN IF EXISTS oauth_subject,
    DROP COLUMN IF EXISTS oauth_handle,
    DROP COLUMN IF EXISTS oauth_email,
    DROP COLUMN IF EXISTS oauth_email_verified;
