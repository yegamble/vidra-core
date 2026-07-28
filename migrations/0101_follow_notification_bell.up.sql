-- 0101: per-channel notification bell + the "new video from a channel you follow"
-- fan-out guard. This closes the deferral recorded on the P8 notification item
-- ("new-video-from-subscription fan-out"); web-push delivery stays deferred.
--
--   * channel_follows.notification_setting — the bell. 'all' notifies the
--     follower every time the channel publishes a new PUBLIC video; 'none' is
--     the muted bell (they keep the subscription, they just stop hearing about
--     it). Default 'all': following a channel means wanting to hear about its
--     videos, matching PeerTube, and the per-user notification_prefs row for the
--     'new_video' type remains the master switch layered on top. Existing
--     follows inherit the default, so no backfill is needed.
--     YouTube's third mode ("personalized") is deliberately NOT offered: we have
--     no personalisation engine, and a mode that silently behaves like one of the
--     other two would be a lie in the UI.
--   * channel_follows_notify_idx serves the fan-out's only lookup — the 'all'
--     followers of ONE channel — so publishing does not scan the whole table.
--   * notifications_new_video_unique_idx makes "at most one new_video
--     notification per (recipient, video)" a DATABASE guarantee rather than an
--     application convention, so a publish hook that fires more than once for the
--     same video (release of a publish-after-transcode hold, moderator approval
--     of a quarantined upload, a retry) can never double-notify. It is scoped to
--     type = 'new_video' so the types that legitimately repeat per video — one
--     comment notification per comment, for instance — are untouched.
ALTER TABLE channel_follows
    ADD COLUMN notification_setting TEXT NOT NULL DEFAULT 'all'
        CHECK (notification_setting IN ('all', 'none'));

CREATE INDEX channel_follows_notify_idx
    ON channel_follows (channel_id)
    WHERE notification_setting = 'all';

CREATE UNIQUE INDEX notifications_new_video_unique_idx
    ON notifications (user_id, video_id)
    WHERE type = 'new_video';
