-- 0043: Per-user notification preferences (product-decisions.md §7). One row per
-- (user, type) with an enabled flag; ABSENCE of a row means the default (enabled),
-- so existing users need no backfill and new notification types default on.
-- notification.Create consults this table before writing a notification.
CREATE TABLE notification_prefs (
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type       TEXT        NOT NULL,
    enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, type)
);
