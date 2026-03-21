-- +goose Up
CREATE TABLE IF NOT EXISTS server_following (
    id          TEXT        PRIMARY KEY,
    host        TEXT        NOT NULL,
    state       TEXT        NOT NULL DEFAULT 'pending',
    follower    BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (host, follower)
);

-- +goose Down
DROP TABLE IF EXISTS server_following;
