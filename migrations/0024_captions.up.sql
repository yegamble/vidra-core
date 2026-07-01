-- 0024: Captions (subtitles). A video owner uploads WebVTT caption tracks for a
-- video; viewers list and download them on the watch page. One track per language
-- per video (re-uploading a language replaces it). The .vtt file itself lives in
-- the media storage backend (key captions/<video_id>/<language>.vtt); this table
-- holds the metadata.
CREATE TABLE captions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id    UUID NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
    language    TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    storage_key TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (video_id, language)
);

CREATE INDEX captions_video_id_idx ON captions (video_id);
