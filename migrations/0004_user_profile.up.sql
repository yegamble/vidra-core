-- 0004: account profile fields. display_name is the human-facing name shown on
-- the account page (username stays the stable login/handle identifier); bio is a
-- short free-text description. Both default to empty so existing rows are valid.

ALTER TABLE users
    ADD COLUMN display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN bio          TEXT NOT NULL DEFAULT '';
