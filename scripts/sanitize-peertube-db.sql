-- sanitize-peertube-db.sql
-- Sanitizes a PeerTube production database dump for staging use.
-- Strips PII while preserving data structure for Migration ETL testing.
--
-- Usage:
--   pg_dump peertube_prod | psql staging_db
--   psql staging_db -f scripts/sanitize-peertube-db.sql
--
-- Idempotent: safe to run multiple times.

BEGIN;

-- 1. Replace emails with synthetic addresses (prevents rainbow-table de-anonymization)
UPDATE "user"
SET email = 'user_' || id || '@sanitized.local'
WHERE email NOT LIKE '%@sanitized.local';

-- 2. Clear password hashes (users must reset password after migration)
-- Uses invalid bcrypt hash that will never match any input (safer than NULL which may violate NOT NULL)
UPDATE "user"
SET password = '$2a$10$sanitized000000000000000000000000000000000000000000'
WHERE password != '$2a$10$sanitized000000000000000000000000000000000000000000';

-- 3. Delete OAuth tokens
DELETE FROM "oAuthToken";

-- 4. Delete OAuth client secrets (keep client records for structure)
UPDATE "oAuthClient"
SET "clientSecret" = 'sanitized-' || id
WHERE "clientSecret" != 'sanitized-' || id;

-- 5. Delete 2FA (TOTP) secrets
UPDATE "user"
SET "otpSecret" = NULL
WHERE "otpSecret" IS NOT NULL;

-- 6. Clear notification preferences (reset to defaults)
UPDATE "userNotificationSetting"
SET "newVideoFromSubscription" = 1,
    "newCommentOnMyVideo" = 1,
    "abuseAsModerator" = 1,
    "videoAutoBlacklistAsModerator" = 1,
    "blacklistOnMyVideo" = 1,
    "myVideoPublished" = 1,
    "myVideoImportFinished" = 1,
    "newFollow" = 1,
    "newUserRegistration" = 1,
    "commentMention" = 1,
    "newInstanceFollower" = 1,
    "autoInstanceFollowing" = 1;

-- 7. Clear session data
DELETE FROM "userVideoHistory";

-- 8. Verify sanitization (informational output)
DO $$
DECLARE
    real_emails INTEGER;
    passwords_remaining INTEGER;
    oauth_tokens INTEGER;
BEGIN
    SELECT COUNT(*) INTO real_emails FROM "user" WHERE email NOT LIKE '%@sanitized.local';
    SELECT COUNT(*) INTO passwords_remaining FROM "user" WHERE password != '$2a$10$sanitized000000000000000000000000000000000000000000';
    SELECT COUNT(*) INTO oauth_tokens FROM "oAuthToken";

    RAISE NOTICE 'Sanitization complete:';
    RAISE NOTICE '  Real emails remaining: %', real_emails;
    RAISE NOTICE '  Password hashes remaining: %', passwords_remaining;
    RAISE NOTICE '  OAuth tokens remaining: %', oauth_tokens;

    IF real_emails > 0 OR passwords_remaining > 0 OR oauth_tokens > 0 THEN
        RAISE WARNING 'SANITIZATION INCOMPLETE — check results above';
    END IF;
END $$;

COMMIT;
