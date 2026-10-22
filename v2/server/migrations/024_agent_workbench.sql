-- 024: one-level agent self-service withdrawals, account snapshot and review.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS payout_method TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS payout_account TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS payout_name TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS payout_updated_at TIMESTAMPTZ;

ALTER TABLE agent_payouts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'paid';
ALTER TABLE agent_payouts ADD COLUMN IF NOT EXISTS requested_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE agent_payouts ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
ALTER TABLE agent_payouts ADD COLUMN IF NOT EXISTS review_note TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_payouts ADD COLUMN IF NOT EXISTS reference TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_payouts ADD COLUMN IF NOT EXISTS account_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX IF NOT EXISTS agent_payouts_status_idx ON agent_payouts(status, requested_at DESC);
CREATE INDEX IF NOT EXISTS agent_payouts_agent_status_idx ON agent_payouts(agent_id, status, requested_at DESC);
