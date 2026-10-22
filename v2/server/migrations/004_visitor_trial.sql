-- 004_visitor_trial.sql — V2：匿名会话身份 + 免费试用 + 票归属（纯增量，可回滚）

-- 票（邀请码）的归属会话：跨设备迁移 / 找回 / 匿名购买入账都靠它
ALTER TABLE invite_codes ADD COLUMN IF NOT EXISTS bound_visitor TEXT;
CREATE INDEX IF NOT EXISTS idx_invite_visitor ON invite_codes(bound_visitor);

-- 匿名会话（签名 cookie 承载；这里是服务端侧记录，用于试用计数）
CREATE TABLE IF NOT EXISTS visitors (
  id         TEXT PRIMARY KEY,
  trial_used INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen  TIMESTAMPTZ NOT NULL DEFAULT now());

-- 草稿：用户输入是最高价值资产，属于会话，永不丢失
CREATE TABLE IF NOT EXISTS drafts (
  visitor_id TEXT PRIMARY KEY,
  payload    JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now());

-- 任务：归属会话 / 来源 IP / 是否试用 / 解锁消耗的票 / 是否已返还
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS visitor    TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS ip         TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS trial      BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS quota_code TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS refunded   BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_jobs_visitor ON jobs(visitor, created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_trial   ON jobs(trial, created_at);

-- 订单：归属会话（匿名购买入账时把票发到该会话名下）
ALTER TABLE orders ADD COLUMN IF NOT EXISTS visitor TEXT;
CREATE INDEX IF NOT EXISTS idx_orders_visitor ON orders(visitor, created_at);
