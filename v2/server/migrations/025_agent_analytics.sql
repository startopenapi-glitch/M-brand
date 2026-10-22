ALTER TABLE analytics_events ADD COLUMN IF NOT EXISTS agent_code TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS analytics_events_agent_idx ON analytics_events(agent_code, day);
