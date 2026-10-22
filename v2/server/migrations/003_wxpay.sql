-- 微信支付 v3 订单表（金额一律整数分）
DROP TABLE IF EXISTS payments;
CREATE TABLE IF NOT EXISTS orders (
  id TEXT PRIMARY KEY,                 -- out_trade_no（≤32位，[A-Za-z0-9_-]）
  code TEXT,                           -- 支付成功后加额度的邀请码
  amount_cents BIGINT NOT NULL,
  channel TEXT NOT NULL,               -- native | h5
  status TEXT NOT NULL DEFAULT 'pending',  -- pending/paid/closed/expired/failed
  code_url TEXT, h5_url TEXT,
  wx_transaction_id TEXT,
  paid_at TIMESTAMPTZ,
  credited BOOLEAN NOT NULL DEFAULT false,
  mock BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status, created_at);
CREATE INDEX IF NOT EXISTS idx_orders_code ON orders(code, created_at);
