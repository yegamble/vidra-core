-- PeerTube seed data for migration ETL integration testing.
-- Minimal schema matching PeerTube's real table/column layout (integer IDs,
-- quoted camelCase column names, numeric enums).

-- ============================================================================
-- Schema
-- ============================================================================

CREATE TABLE IF NOT EXISTS actor (
    id          SERIAL PRIMARY KEY,
    type        VARCHAR(50) NOT NULL DEFAULT 'Person',
    url         TEXT,
    "preferredUsername" VARCHAR(255),
    "createdAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS "user" (
    id          SERIAL PRIMARY KEY,
    username    VARCHAR(255) NOT NULL UNIQUE,
    email       VARCHAR(255) NOT NULL UNIQUE,
    role        INTEGER NOT NULL DEFAULT 2,  -- 0=admin, 1=moderator, 2=user
    blocked     BOOLEAN NOT NULL DEFAULT FALSE,
    "emailVerified" BOOLEAN DEFAULT TRUE,
    "createdAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account (
    id          SERIAL PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    "userId"    INTEGER REFERENCES "user"(id),
    "actorId"   INTEGER REFERENCES actor(id),
    description TEXT,
    "createdAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS "videoChannel" (
    id          SERIAL PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    description TEXT,
    support     TEXT,
    "accountId" INTEGER NOT NULL REFERENCES account(id),
    "actorId"   INTEGER REFERENCES actor(id),
    "createdAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS video (
    id          SERIAL PRIMARY KEY,
    uuid        UUID NOT NULL DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    description TEXT,
    privacy     INTEGER NOT NULL DEFAULT 1,  -- 1=public, 2=unlisted, 3=private
    duration    INTEGER NOT NULL DEFAULT 0,
    views       INTEGER NOT NULL DEFAULT 0,
    language    VARCHAR(10),
    "channelId" INTEGER NOT NULL REFERENCES "videoChannel"(id),
    remote      BOOLEAN NOT NULL DEFAULT FALSE,
    state       INTEGER NOT NULL DEFAULT 1,  -- 1=published
    "publishedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "createdAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS "videoComment" (
    id          SERIAL PRIMARY KEY,
    text        TEXT NOT NULL,
    "videoId"   INTEGER NOT NULL REFERENCES video(id),
    "accountId" INTEGER NOT NULL REFERENCES account(id),
    "inReplyToCommentId" INTEGER REFERENCES "videoComment"(id),
    "deletedAt" TIMESTAMP WITH TIME ZONE,
    "createdAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS "videoPlaylist" (
    id          SERIAL PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    description TEXT,
    privacy     INTEGER NOT NULL DEFAULT 1,
    "ownerAccountId" INTEGER NOT NULL REFERENCES account(id),
    "createdAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt" TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS "videoPlaylistElement" (
    id                  SERIAL PRIMARY KEY,
    "videoPlaylistId"   INTEGER NOT NULL REFERENCES "videoPlaylist"(id),
    "videoId"           INTEGER NOT NULL REFERENCES video(id),
    position            INTEGER NOT NULL DEFAULT 1,
    "createdAt"         TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt"         TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS "videoCaption" (
    id              SERIAL PRIMARY KEY,
    "videoId"       INTEGER NOT NULL REFERENCES video(id),
    language        VARCHAR(10) NOT NULL,
    filename        VARCHAR(255),
    "fileUrl"       TEXT,
    "createdAt"     TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    "updatedAt"     TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- ============================================================================
-- Seed data
-- ============================================================================

-- Actors (one per user + one per channel)
INSERT INTO actor (id, type, "preferredUsername") VALUES
    (1, 'Person',  'admin_user'),
    (2, 'Person',  'alice'),
    (3, 'Person',  'bob'),
    (4, 'Group',   'admin_channel'),
    (5, 'Group',   'alice_channel');

SELECT setval('actor_id_seq', 5);

-- Users: 1 admin (role 0), 2 regular (role 2)
INSERT INTO "user" (id, username, email, role, blocked, "createdAt") VALUES
    (1, 'admin_user', 'admin@peertube.example', 0, FALSE, '2024-01-01 00:00:00+00'),
    (2, 'alice',      'alice@peertube.example', 2, FALSE, '2024-02-01 00:00:00+00'),
    (3, 'bob',        'bob@peertube.example',   2, TRUE,  '2024-03-01 00:00:00+00');

SELECT setval('user_id_seq', 3);

-- Accounts (one per user)
INSERT INTO account (id, name, "userId", "actorId") VALUES
    (1, 'admin_user', 1, 1),
    (2, 'alice',      2, 2),
    (3, 'bob',        3, 3);

SELECT setval('account_id_seq', 3);

-- Channels: 2 channels (admin owns 1, alice owns 1)
INSERT INTO "videoChannel" (id, name, description, support, "accountId", "actorId", "createdAt") VALUES
    (1, 'admin_channel', 'Admin official channel', 'Support us', 1, 4, '2024-01-15 00:00:00+00'),
    (2, 'alice_channel', 'Alice creative hub',     NULL,         2, 5, '2024-02-15 00:00:00+00');

SELECT setval(pg_get_serial_sequence('"videoChannel"', 'id'), 2);

-- Videos: 3 local videos (2 public, 1 unlisted)
INSERT INTO video (id, uuid, name, description, privacy, duration, views, language, "channelId", remote, "publishedAt", "createdAt") VALUES
    (1, 'a0000000-0000-0000-0000-000000000001', 'Getting Started',   'A tutorial video',  1, 300, 1500, 'en', 1, FALSE, '2024-01-20 00:00:00+00', '2024-01-20 00:00:00+00'),
    (2, 'a0000000-0000-0000-0000-000000000002', 'Art of Painting',   'Creative deep dive', 2, 600, 800,  'fr', 2, FALSE, '2024-02-20 00:00:00+00', '2024-02-20 00:00:00+00'),
    (3, 'a0000000-0000-0000-0000-000000000003', 'Advanced Concepts', 'In-depth analysis',  1, 900, 3200, 'en', 1, FALSE, '2024-03-20 00:00:00+00', '2024-03-20 00:00:00+00');

SELECT setval('video_id_seq', 3);

-- Comments: 4 total (2 top-level + 2 replies for thread testing)
INSERT INTO "videoComment" (id, text, "videoId", "accountId", "inReplyToCommentId", "createdAt") VALUES
    (1, 'Great tutorial, thanks!',          1, 2, NULL, '2024-01-21 00:00:00+00'),
    (2, 'Nice work on the visuals.',        2, 1, NULL, '2024-02-21 00:00:00+00'),
    (3, 'I agree, very helpful!',           1, 3, 1,    '2024-01-22 00:00:00+00'),
    (4, 'Could you do a follow-up video?',  1, 2, 1,    '2024-01-23 00:00:00+00');

SELECT setval(pg_get_serial_sequence('"videoComment"', 'id'), 4);

-- Playlist: 1 playlist owned by alice with 2 items
INSERT INTO "videoPlaylist" (id, name, description, privacy, "ownerAccountId", "createdAt") VALUES
    (1, 'Favorites', 'My favorite videos', 1, 2, '2024-03-01 00:00:00+00');

SELECT setval(pg_get_serial_sequence('"videoPlaylist"', 'id'), 1);

INSERT INTO "videoPlaylistElement" (id, "videoPlaylistId", "videoId", position) VALUES
    (1, 1, 1, 1),
    (2, 1, 3, 2);

SELECT setval(pg_get_serial_sequence('"videoPlaylistElement"', 'id'), 2);

-- Captions: 2 captions on different videos
INSERT INTO "videoCaption" (id, "videoId", language, filename) VALUES
    (1, 1, 'en', 'a0000000-0000-0000-0000-000000000001-en.vtt'),
    (2, 2, 'fr', 'a0000000-0000-0000-0000-000000000002-fr.vtt');

SELECT setval(pg_get_serial_sequence('"videoCaption"', 'id'), 2);
