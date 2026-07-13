-- 0093: Per-user search & recommendation preferences (search-service W4,
-- MASTER-PLAN §2.1). These are the user half of the two-factor personalization
-- gate: the EFFECTIVE personalization/history flags core computes per request are
-- instance-setting AND user-pref AND signed-in. All default TRUE, preserving the
-- opt-out (never opt-in) posture of the existing history_enabled flag; a user can
-- turn each off from the search & recommendations settings page.
ALTER TABLE users
    ADD COLUMN search_history_enabled               BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN personalized_search_enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN personalized_recommendations_enabled BOOLEAN NOT NULL DEFAULT TRUE;
