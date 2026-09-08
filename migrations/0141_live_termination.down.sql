-- Reverse 0141. The constraint goes with the columns it constrains.
ALTER TABLE live_streams
    DROP CONSTRAINT IF EXISTS live_streams_termination_reason_code_check;

ALTER TABLE live_streams
    DROP COLUMN IF EXISTS terminated_at,
    DROP COLUMN IF EXISTS terminated_by,
    DROP COLUMN IF EXISTS termination_reason_code,
    DROP COLUMN IF EXISTS termination_reason;
