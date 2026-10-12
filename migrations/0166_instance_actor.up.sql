-- 0166: the instance actor and instance follows (PeerTube parity).
--
-- PeerTube federates instance-to-instance through one `Application` actor,
-- `peertube`, served at /accounts/peertube. An admin follows another instance;
-- the follower's instance actor sends Follow to the remote instance actor, and
-- the followed instance Announces its public videos to it. Vidra had no such
-- actor, so a PeerTube admin could not federate with a vidra instance the way
-- they federate with each other. This migration carries the whole schema for
-- that, so the stack of PRs that uses it adds no further migration.
--
-- (1) The keypair. One row, ever: the singleton key makes a second row
-- unrepresentable. Same storage contract as account_actor_keys (0035): the
-- private key is sealed under FEDERATION_KEY_KEK, or raw PEM in dev only, and
-- it is minted lazily on first use.
CREATE TABLE instance_actor_keys (
    singleton       BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    public_key_pem  TEXT        NOT NULL,
    private_key_pem TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- (2) The name. `peertube` is the name PeerTube itself looks for: its
-- auto-follow-back matches `type = Application AND preferredUsername =
-- peertube`, and following "an instance by host" WebFingers acct:peertube@host.
-- It joins the one handle namespace (0142) as a third kind of holder, so no
-- account or channel can take it on ANY write path — registration, admin
-- creation, OAuth-derived usernames and importers all go through the same
-- reservation trigger. Widening the CHECK (drop + re-add under the same name)
-- keeps every row the previous release wrote valid.
--
-- ON CONFLICT DO NOTHING: an instance where an account already holds the name
-- keeps that account exactly as it was (a username is a sign-in identifier and
-- is never renamed). The application then serves that account, not the
-- instance actor, and instance follows report themselves unavailable.
ALTER TABLE actor_handles ADD COLUMN instance_actor BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE actor_handles DROP CONSTRAINT actor_handles_one_subject;
ALTER TABLE actor_handles ADD CONSTRAINT actor_handles_one_subject
    CHECK (num_nonnulls(user_id, channel_id) + instance_actor::int = 1);
INSERT INTO actor_handles (handle_lower, instance_actor) VALUES ('peertube', TRUE)
ON CONFLICT DO NOTHING;

-- (3) The delivery queue can sign as the instance actor: a third signer beside
-- the channel (0038) and account (0052) columns. Exactly one is set per row.
ALTER TABLE federation_deliveries
    ADD COLUMN signing_instance_actor BOOLEAN NOT NULL DEFAULT FALSE;

-- (4) Inbound: remote instance actors following ours. 'pending' waits for an
-- admin while federation_follower_approval is on (PeerTube's instance-follower
-- approval); 'rejected' is sticky, as in PeerTube, so a re-Follow from an
-- instance the admin refused is answered with a Reject again rather than
-- re-queued. The follower is a cached remote actor and goes with it.
CREATE TABLE instance_followers (
    id                  UUID        NOT NULL UNIQUE DEFAULT uuid_generate_v4(),
    remote_actor_url    TEXT        PRIMARY KEY REFERENCES remote_actors (actor_url) ON DELETE CASCADE,
    state               TEXT        NOT NULL DEFAULT 'pending'
                                    CHECK (state IN ('pending', 'accepted', 'rejected')),
    follow_activity_url TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX instance_followers_listing_idx ON instance_followers (created_at DESC, id DESC);

-- (5) Outbound: our instance actor following remote instance actors. `host` is
-- what the admin typed and what the ingestion gate matches on: following an
-- instance means asking for every public video it announces, whichever of its
-- actors signs the Create/Update.
CREATE TABLE instance_following (
    id                  UUID        NOT NULL UNIQUE DEFAULT uuid_generate_v4(),
    remote_actor_url    TEXT        PRIMARY KEY REFERENCES remote_actors (actor_url) ON DELETE CASCADE,
    host                TEXT        NOT NULL,
    state               TEXT        NOT NULL DEFAULT 'pending'
                                    CHECK (state IN ('pending', 'accepted', 'rejected')),
    follow_activity_url TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX instance_following_activity_idx ON instance_following (follow_activity_url);
CREATE INDEX instance_following_accepted_host_idx ON instance_following (lower(host)) WHERE state = 'accepted';
CREATE INDEX instance_following_listing_idx ON instance_following (created_at DESC, id DESC);

-- (6) The staff push signal for a pending instance follower reuses 0156's
-- federation_follower_request type with no subject row: it points at the
-- queue, not at one entry, so at most one UNREAD notice per admin however many
-- instances wait — a database guarantee, as 0156's per-subject indexes are.
CREATE UNIQUE INDEX notifications_federation_instance_follower_request_unread_idx
    ON notifications (user_id)
    WHERE type = 'federation_follower_request' AND remote_follow_id IS NULL AND read_at IS NULL;
