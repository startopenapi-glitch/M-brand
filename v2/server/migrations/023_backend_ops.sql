-- 023: admin 2FA, worker heartbeat, after-sales and funnel analytics.

ALTER TABLE admins ADD COLUMN IF NOT EXISTS totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE admins ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE admins ADD COLUMN IF NOT EXISTS session_version BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS worker_heartbeats (
  worker TEXT PRIMARY KEY,
  instance_id TEXT NOT NULL,
  last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
  details JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS support_cases (
  id TEXT PRIMARY KEY,
  request_key TEXT NOT NULL UNIQUE,
  visitor TEXT NOT NULL,
  job_id TEXT,
  order_id TEXT,
  category TEXT NOT NULL DEFAULT 'other',
  description TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'open',
  resolution TEXT NOT NULL DEFAULT '',
  version BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS support_cases_visitor_idx ON support_cases(visitor, created_at DESC);
CREATE INDEX IF NOT EXISTS support_cases_status_idx ON support_cases(status, created_at DESC);

CREATE TABLE IF NOT EXISTS support_events (
  id BIGSERIAL PRIMARY KEY,
  case_id TEXT NOT NULL REFERENCES support_cases(id) ON DELETE CASCADE,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS analytics_events (
  id BIGSERIAL PRIMARY KEY,
  day DATE NOT NULL DEFAULT current_date,
  event TEXT NOT NULL,
  visitor_hash TEXT NOT NULL DEFAULT '',
  job_id TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS analytics_events_day_event_idx ON analytics_events(day, event);
CREATE INDEX IF NOT EXISTS analytics_events_visitor_idx ON analytics_events(visitor_hash, day);
