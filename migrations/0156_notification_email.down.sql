-- 0156 down: owed emails and the email switch go; in-app prefs are untouched.
DROP TABLE IF EXISTS notification_emails;
ALTER TABLE notification_prefs DROP COLUMN IF EXISTS email_enabled;
