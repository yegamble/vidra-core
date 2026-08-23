-- Rollback 0111: drop the CENC content-key table.
--
-- This is DESTRUCTIVE in a way most rollbacks are not: the sealed keys are the
-- only thing that can decrypt already-packaged media, and no other copy exists.
-- Rolling back on an install that has actually encrypted content orphans that
-- content permanently. On every install that has not (all of them, in this
-- slice) the table is empty and the rollback costs nothing.
DROP TABLE IF EXISTS video_drm_keys;
