-- Additive recovery metadata only. Existing jobs and commercial rules are unchanged.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS creation_owner TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS creation_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS jobs_creation_intent_unique
 ON jobs(creation_owner, creation_key) WHERE creation_key IS NOT NULL;
