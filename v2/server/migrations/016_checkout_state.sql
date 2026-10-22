-- 2026-10-21: 显式记录微信 Native 下单准备状态，避免 pending + 空 code_url 的订单
-- 既没有二维码又占住任务的下单入口。历史 pending 无二维码订单标记为 unknown，
-- 由显式恢复/对账处理，不能直接判定为 failed 后重复收款。
ALTER TABLE orders ADD COLUMN IF NOT EXISTS checkout_state TEXT NOT NULL DEFAULT 'ready';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS checkout_error TEXT NOT NULL DEFAULT '';

UPDATE orders
SET checkout_state = 'unknown'
WHERE status = 'pending' AND COALESCE(code_url, '') = '';
