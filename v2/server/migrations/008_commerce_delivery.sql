-- Additive; prices and sold rights are never rewritten.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS delivery_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS agent_id TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS agent_pct INT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS checkout_started BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX IF NOT EXISTS agent_commission_order_once ON agent_commissions(order_id);
CREATE INDEX IF NOT EXISTS orders_job_state ON orders(job_id,status);
CREATE INDEX IF NOT EXISTS orders_reconcile ON orders(status,updated_at);
CREATE TABLE IF NOT EXISTS delivery_items (
 job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
 kind TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','done','error')),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(job_id,kind)
);
