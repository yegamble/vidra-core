-- 0138: two facts federation could not represent (A29 remediation).
--
-- 0137 belongs to the A33 purge-jobs slice; this is the next free number.
--
-- (1) federated_video_tombstones. A29, from the federation side: after a video
-- is deleted, its ActivityPub `id` still answers HTTP 200 — the frontend shell
-- rendering "Video not found" client-side. A peer dereferencing the retraction
-- it was just sent therefore gets a PAGE, learns nothing, and has no way to
-- distinguish "deleted" from "this server is confused". ActivityPub's answer is
-- 410 Gone with a Tombstone object, and answering that needs the one thing a
-- deleted row cannot provide: a record that this id ONCE existed. Without it a
-- 410 would have to be the answer for every well-formed uuid, which would tell
-- a peer that every video it has never heard of was deleted.
--
-- The row is deliberately tiny — an id and a time — because that is the entire
-- content of a Tombstone. It carries no title, no channel and no reason: a
-- retraction that leaked the metadata of the thing retracted would defeat the
-- deletion. Rows are written for PUBLIC videos only (the only ones that were
-- ever federated), by the same delete path that fans out the Delete activity.
CREATE TABLE federated_video_tombstones (
    video_id   UUID        PRIMARY KEY,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Retention sweeps and the "was this recently deleted?" read.
CREATE INDEX federated_video_tombstones_deleted_idx
    ON federated_video_tombstones (deleted_at DESC);

-- (2) remote_actor_blocks. A29 measured that per-account moderation stops at
-- the local boundary: /me/blocks/{id} and /me/mutes/accounts/{id} both take a
-- LOCAL user uuid, and muted_accounts.muted_id is a users FK, so the only
-- control a viewer has against a remote PERSON is blocking their entire
-- instance. That is a sledgehammer — one troll costs the viewer every creator
-- on that server.
--
-- The remote side of a block is addressed by ACTOR URL rather than by a
-- remote_actors FK on purpose: a viewer must be able to block an actor this
-- instance has never cached (the actor is named in an activity before it is
-- resolved), and a later actor-cache eviction must not silently lift the block.
-- The URL is the identity the fediverse itself uses.
CREATE TABLE remote_actor_blocks (
    blocker_id       UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    remote_actor_url TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, remote_actor_url)
);

-- List a viewer's remote blocks newest-first (the settings surface).
CREATE INDEX remote_actor_blocks_blocker_idx
    ON remote_actor_blocks (blocker_id, created_at DESC);
-- Reverse lookup: "is this actor blocked by anyone whose content it is about to
-- reach?" — the inbound Note/Follow refusal.
CREATE INDEX remote_actor_blocks_actor_idx
    ON remote_actor_blocks (remote_actor_url);
