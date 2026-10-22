-- Additive execution metadata. No commercial prices, rights or ledger changes.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS execution_token TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS execution_started_at TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS generation_attempts (
 job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
 kind TEXT NOT NULL, idx INT NOT NULL, state TEXT NOT NULL,
 started_at TIMESTAMPTZ NOT NULL DEFAULT now(), finished_at TIMESTAMPTZ,
 PRIMARY KEY(job_id,kind,idx),
 CHECK(state IN ('submitted','unknown','failed','done'))
);
CREATE INDEX IF NOT EXISTS jobs_recovery_idx ON jobs(updated_at) WHERE status IN ('running','boards_running');
