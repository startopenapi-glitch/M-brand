-- 026: email invitations for one-level agents; payout methods switch to Alipay/bank.
CREATE TABLE IF NOT EXISTS agent_invitations (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  name TEXT NOT NULL,
  commission_pct INT NOT NULL DEFAULT 40,
  token_hash TEXT NOT NULL UNIQUE,
  invited_by TEXT NOT NULL DEFAULT '',
  expires_at TIMESTAMPTZ NOT NULL,
  accepted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_invitations_email_idx ON agent_invitations(email);
UPDATE agents SET payout_method='alipay' WHERE payout_method='wechat';
