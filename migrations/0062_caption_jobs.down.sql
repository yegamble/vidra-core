-- Roll back 0062: drop the auto-caption (Whisper) job queue.
DROP TABLE IF EXISTS caption_jobs;
