-- Record how a job's directions were unlocked; all fields additive.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS unlock_method TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS unlocked_at TIMESTAMPTZ;
