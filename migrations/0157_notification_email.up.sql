-- 0157: notification EMAIL — the per-type email switch and the outbox that
-- delivers it. PeerTube gives every notification type independent web and email
-- settings, and email is its main channel for bringing a user back; until now
-- vidra's notifications were in-app only.
--
--   * notification_prefs.email_enabled — the EMAIL switch beside the in-app
--     `enabled`. NULL means "the type's default", exactly as an absent row
--     already means the in-app default, so nobody needs a backfill and an
--     in-app toggle never disturbs the email choice (or the reverse). Email is
--     OPT-IN for every type that offers it (comment, comment_reply, new_video),
--     mirroring PeerTube's defaults for the equivalent types, which are web-only
--     (server/core/lib/user.ts: newCommentOnMyVideo, commentMention,
--     newVideoFromSubscription = WEB). Only an explicit TRUE ever mails.
--   * notification_emails — the outbox. A row means "this in-app notification
--     is ALSO owed an email". It is written by the same statement that creates
--     the notification (set-based for the new_video fan-out), so a popular
--     channel's publish costs one round trip and sends nothing inline; a
--     background drain mails it with retries and deletes the row once it is
--     sent or no longer deliverable. notification_id is the key, so a
--     notification is owed at most one email, and the cascade drops the debt
--     with the notification (or the user) it belongs to.
ALTER TABLE notification_prefs ADD COLUMN email_enabled BOOLEAN;

CREATE TABLE notification_emails (
    notification_id UUID        PRIMARY KEY REFERENCES notifications (id) ON DELETE CASCADE,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The drain's only lookup: what is due, oldest first.
CREATE INDEX notification_emails_due_idx ON notification_emails (next_attempt_at, notification_id);
