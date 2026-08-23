-- 0111: CENC content keys for DRM-protected videos (phase-5 enterprise,
-- docs/productionization/interfaces.md §10).
--
-- §10's rule is "content keys never in the normal DB". This table is where that
-- rule is DRAWN rather than where it is broken: the plaintext content key never
-- exists in any queryable column here. What is stored is the key SEALED under
-- DRM_KEY_KEK with internal/secretbox (AES-256-GCM, "enc:" prefix) — the same
-- envelope discipline that already holds ActivityPub actor private keys, TOTP
-- secrets and linked Bluesky app passwords. A dump of this table, a replica, a
-- backup or a compromised read-only role yields ciphertext and nothing else;
-- opening it additionally requires an environment variable that is never in the
-- database at all.
--
-- DRM_KEY_KEK deliberately does NOT fall back to FEDERATION_KEY_KEK the way
-- MFA_KEY_KEK and ATPROTO_KEY_KEK do. Those seal credentials this instance
-- holds on a user's behalf; a content key is the thing an entire DRM deployment
-- exists to protect, and sharing a KEK with the federation actor keys would put
-- both trust domains behind one secret. internal/config refuses the fallback
-- explicitly, with that reason in the error text.
--
-- ONE ROW PER VIDEO, and video_id is therefore the primary key rather than a
-- foreign key with its own surrogate id — the sidecar shape streaming_playlists
-- (0039) already uses. Per-TRACK keys (a separate key for audio, or per
-- rendition) are a real DRM feature and are deliberately NOT modelled yet:
-- adding them later is a new table or a widened key, whereas guessing at the
-- shape now would ship a schema nothing writes.
--
-- Nothing writes this table yet. The packaging step that would call
-- PrepareAsset does not exist in this slice, so on every existing install this
-- table stays empty and every DRM read path reports "no protection" — which is
-- exactly the shipped, clear-media behaviour.
CREATE TABLE video_drm_keys (
    -- The subject. ON DELETE CASCADE ties the key's lifetime to the video's:
    -- a deleted video must not leave a content key behind, and a content key
    -- with no video is unreachable anyway.
    video_id           uuid        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,

    -- The key id (KID). PUBLIC by design: it travels in the manifest, in the
    -- PSSH box, in the EME `encrypted` event and in the license request body.
    -- It names a key; it is not one. A UUID because a CENC KID is exactly 16
    -- bytes, which is what a UUID is, so no second encoding has to be agreed on
    -- between the packager, the manifest and the license endpoint.
    key_id             uuid        NOT NULL,

    -- The 16-byte AES content key, SEALED. Never a plaintext key, never a
    -- reversible encoding of one: internal/secretbox.Seal output, which is
    -- "enc:" + base64(nonce || AES-256-GCM ciphertext). TEXT rather than BYTEA
    -- because that is the shape every other sealed column in this schema uses
    -- and because the "enc:" prefix is what lets a human (or a doctor check)
    -- tell a sealed value from an unsealed one at a glance.
    content_key_sealed text        NOT NULL,

    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE video_drm_keys IS
    'CENC content keys, sealed under DRM_KEY_KEK (internal/secretbox). The plaintext key exists only in process memory; it is never stored in, and never returned from, any queryable column.';
COMMENT ON COLUMN video_drm_keys.key_id IS
    'Public CENC key id (KID). Travels in manifests and license requests; names a key rather than being one.';
COMMENT ON COLUMN video_drm_keys.content_key_sealed IS
    'SECRET. secretbox-sealed 16-byte AES content key ("enc:" + base64(nonce||ciphertext)). Never logged, never returned by any API.';
