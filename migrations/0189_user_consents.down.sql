ALTER TABLE registration_requests
    DROP COLUMN consent_accepted_at,
    DROP COLUMN consent_terms_url,
    DROP COLUMN consent_terms_version,
    DROP COLUMN consent_privacy_url,
    DROP COLUMN consent_privacy_version,
    DROP COLUMN consent_minimum_age;

DROP TABLE user_consents;
DROP FUNCTION user_consents_refuse_update();
