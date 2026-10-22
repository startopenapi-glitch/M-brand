-- Operational metadata only: clocks follow a stage / purchased delivery version;
-- fair bounded recovery persists its scan cursor across service restarts.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS execution_stage TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS execution_version BIGINT NOT NULL DEFAULT -1;
ALTER TABLE generation_attempts ADD COLUMN IF NOT EXISTS last_checked_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS generation_attempts_recovery_idx
 ON generation_attempts(last_checked_at NULLS FIRST,job_id,kind,idx)
 WHERE state IN ('submitted','unknown');
