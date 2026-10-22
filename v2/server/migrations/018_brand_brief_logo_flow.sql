-- 2026-10-21: structured brand brief / directions, formal-logo review state,
-- revision versions and durable generation records. All additive.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS brand_brief JSONB;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS logo_directions JSONB;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS logo_confirmed BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS logo_revision_instruction TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS logo_versions (
  id BIGSERIAL PRIMARY KEY,
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  version INT NOT NULL,
  file TEXT NOT NULL,
  prompt TEXT NOT NULL DEFAULT '',
  parent_file TEXT NOT NULL DEFAULT '',
  instruction TEXT NOT NULL DEFAULT '',
  direction_snapshot JSONB,
  status TEXT NOT NULL DEFAULT 'succeeded',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(job_id,version)
);

CREATE TABLE IF NOT EXISTS generation_records (
  id BIGSERIAL PRIMARY KEY,
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  step TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT '',
  idx INT NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  prompt_template_version TEXT NOT NULL DEFAULT '',
  prompt TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  request_id TEXT NOT NULL DEFAULT '',
  parent_image TEXT NOT NULL DEFAULT '',
  refs JSONB,
  params JSONB,
  response_image TEXT NOT NULL DEFAULT '',
  revised_prompt TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  tokens INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS generation_records_job_idx ON generation_records(job_id,created_at);
