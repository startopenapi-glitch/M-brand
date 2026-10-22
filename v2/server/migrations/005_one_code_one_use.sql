-- 005_one_code_one_use.sql — 一码一次 · 一码一人（2026-10-13 用户确认口径）
-- 新语义：max_uses 恒为 1（单次码）；used_at/used_by/order_id 用于审计与追溯
ALTER TABLE invite_codes ADD COLUMN IF NOT EXISTS used_at  TIMESTAMPTZ; -- 首次使用时间
ALTER TABLE invite_codes ADD COLUMN IF NOT EXISTS used_by  TEXT;        -- 使用会话（审计）
ALTER TABLE invite_codes ADD COLUMN IF NOT EXISTS order_id TEXT;        -- 来源订单（一单一码可追溯）

-- M-brand starts with a clean database; no operator-specific code list is included.
