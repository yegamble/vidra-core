-- 0173: per-flag sensitive content (PeerTube 7.2 nsfwFlags parity).
--   * videos.sensitive_flags / remote_videos.sensitive_flags — WHICH kind of
--     sensitive a video is: violent, shocking, sex. is_sensitive stays the
--     aggregate (the API keeps it true whenever a flag is set) so every
--     pre-flag reader and policy keeps working; the free-text summary is the
--     existing videos.sensitive_reason.
--   * user_content_prefs — a viewer's per-flag policy overrides
--     (flag -> hide|warn|blur|display) layered on users.sensitive_content_policy,
--     and preferred_languages (the viewer's feed language filter, wired by a
--     later change). One row per user; no row = no overrides.
--   * vidra_sensitive_hidden() — THE server-side "hide" predicate for the
--     browse/search queries. An unflagged sensitive video follows the base
--     policy; a flagged one is hidden when ANY of its flags resolves to hide
--     (the viewer's override for that flag, else the base policy).
ALTER TABLE videos
    ADD COLUMN sensitive_flags TEXT[] NOT NULL DEFAULT '{}'
        CHECK (sensitive_flags <@ ARRAY['violent', 'shocking', 'sex']::text[]);
ALTER TABLE remote_videos
    ADD COLUMN sensitive_flags TEXT[] NOT NULL DEFAULT '{}'
        CHECK (sensitive_flags <@ ARRAY['violent', 'shocking', 'sex']::text[]);

CREATE TABLE user_content_prefs (
    user_id                 UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    sensitive_flag_policies JSONB NOT NULL DEFAULT '{}',
    preferred_languages     TEXT[] NOT NULL DEFAULT '{}',
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION vidra_sensitive_hidden(is_sensitive boolean, flags text[], base_hide boolean, viewer uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT is_sensitive AND CASE
        WHEN cardinality(flags) = 0 THEN base_hide
        ELSE EXISTS (
            SELECT 1 FROM unnest(flags) AS f
            WHERE COALESCE(
                      (SELECT p.sensitive_flag_policies ->> f FROM user_content_prefs p WHERE p.user_id = viewer),
                      CASE WHEN base_hide THEN 'hide' END) = 'hide')
    END
$$;
