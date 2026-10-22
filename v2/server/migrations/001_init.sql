CREATE TABLE IF NOT EXISTS admins (
  id SERIAL PRIMARY KEY, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE IF NOT EXISTS invite_codes (
  code TEXT PRIMARY KEY, max_uses INT DEFAULT 20, used INT DEFAULT 0,
  note TEXT DEFAULT '', revoked BOOLEAN DEFAULT false, created_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY, code TEXT, form JSONB, status TEXT DEFAULT 'queued',
  stage TEXT DEFAULT 'directions', chosen INT, error TEXT,
  created_at TIMESTAMPTZ DEFAULT now(), updated_at TIMESTAMPTZ DEFAULT now());
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at);
CREATE TABLE IF NOT EXISTS job_images (
  id BIGSERIAL PRIMARY KEY, job_id TEXT, kind TEXT, idx INT,
  file TEXT, prompt TEXT, tokens INT, created_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE IF NOT EXISTS cases (
  id SERIAL PRIMARY KEY, job_id TEXT, title TEXT, tagline TEXT, images JSONB,
  sort INT DEFAULT 0, published BOOLEAN DEFAULT true, created_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE IF NOT EXISTS payments (
  id SERIAL PRIMARY KEY, provider TEXT, order_no TEXT UNIQUE, code TEXT,
  amount_cents INT, status TEXT, raw JSONB, created_at TIMESTAMPTZ DEFAULT now());
CREATE TABLE IF NOT EXISTS usage_events (
  id BIGSERIAL PRIMARY KEY, code TEXT, job_id TEXT, kind TEXT, model TEXT,
  tokens INT, created_at TIMESTAMPTZ DEFAULT now());
CREATE INDEX IF NOT EXISTS idx_usage_time ON usage_events(created_at);
CREATE TABLE IF NOT EXISTS audit_events (
  id BIGSERIAL PRIMARY KEY, actor TEXT, action TEXT, object_type TEXT, object_id TEXT,
  detail JSONB, created_at TIMESTAMPTZ DEFAULT now());
