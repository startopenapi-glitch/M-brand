-- v5 代理分销：代理表 + 佣金记录 + 提现记录
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  wechat TEXT DEFAULT '',
  code TEXT UNIQUE NOT NULL,
  commission_pct INT NOT NULL DEFAULT 40,
  active BOOLEAN NOT NULL DEFAULT true,
  total_earned_cents BIGINT NOT NULL DEFAULT 0,
  total_paid_cents BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_agents_code ON agents(code) WHERE active = true;

CREATE TABLE IF NOT EXISTS agent_commissions (
  id SERIAL PRIMARY KEY,
  agent_id TEXT NOT NULL REFERENCES agents(id),
  order_id TEXT NOT NULL,
  order_amount_cents BIGINT NOT NULL,
  commission_cents BIGINT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','paid')),
  paid_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_agent_commissions_agent ON agent_commissions(agent_id);

CREATE TABLE IF NOT EXISTS agent_payouts (
  id SERIAL PRIMARY KEY,
  agent_id TEXT NOT NULL REFERENCES agents(id),
  amount_cents BIGINT NOT NULL,
  method TEXT NOT NULL DEFAULT 'wechat',
  note TEXT DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE orders ADD COLUMN IF NOT EXISTS agent_code TEXT;
