-- 2026-10-21: freeze the selected direction's mechanism/presentation on the job.
-- New choices write this snapshot; old jobs without it use a read-only fallback
-- from their existing chosen index and form (never reselect or re-render).
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS direction_snapshot JSONB;
