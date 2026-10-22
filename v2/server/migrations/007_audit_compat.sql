-- 分包解锁模式
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS unlocked_packs TEXT[] DEFAULT '{}';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS job_id TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS pack TEXT;

CREATE TABLE IF NOT EXISTS pack_pricing (
  pack TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT DEFAULT '',
  price_cents INT NOT NULL,
  sort_order INT DEFAULT 0
);
INSERT INTO pack_pricing (pack, name, description, price_cents, sort_order) VALUES
  ('logo',   'Logo 资产包',    'Logo变体+使用规范+色彩速查',   990, 1),
  ('system', '品牌视觉系统',   '系统卡+设计系统',             1990, 2),
  ('boards', '品牌提案四图',   '世界观+概念逻辑+视觉系统+应用', 2990, 3),
  ('apps',   '应用落地包',     '名片+文具+社交+PPT+品牌故事',  1990, 4),
  ('all',    '全包（省¥29.6）', '一次解锁全部四个包',           4990, 0)
ON CONFLICT DO NOTHING;

-- 新增图片类型索引
CREATE INDEX IF NOT EXISTS idx_job_images_pack ON job_images(job_id, kind);

-- v4 自选付费：项目定价 + 订单项明细
CREATE TABLE IF NOT EXISTS item_pricing (
  kind TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  category TEXT NOT NULL,
  price_cents INT NOT NULL,
  sort_order INT DEFAULT 0
);
INSERT INTO item_pricing (kind, name, category, price_cents, sort_order) VALUES
  ('logo_h',      'Logo 横版组合',    'logo',   390, 1),
  ('logo_v',      'Logo 竖版组合',    'logo',   390, 2),
  ('logo_icon',   'Logo 图标',        'logo',   390, 3),
  ('logo_mono',   'Logo 单色黑版',    'logo',   190, 4),
  ('logo_white',  'Logo 反白版',      'logo',   190, 5),
  ('color_typo',  '色彩与字体规范',   'system', 290, 6),
  ('photo_style', '摄影与材质风格',   'system', 290, 7),
  ('worldview',   '品牌世界观封面',   'boards', 490, 8),
  ('logo_logic',  'Logo 概念逻辑',   'boards', 390, 9),
  ('app_overview','应用场景总览',     'boards', 390, 10),
  ('card_front',  '名片正面',         'apps',   290, 11),
  ('card_back',   '名片背面',         'apps',   190, 12),
  ('letterhead',  '信纸设计',         'apps',   290, 13),
  ('wx_avatar',   '微信头像',         'apps',   190, 14),
  ('wx_cover',    '公众号封面',       'apps',   290, 15),
  ('ppt_cover',   'PPT 模板封面',     'apps',   290, 16),
  ('brand_story', '品牌故事文档',     'apps',    90, 17)
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS order_items (
  id SERIAL PRIMARY KEY,
  order_id TEXT NOT NULL,
  item_kind TEXT NOT NULL,
  item_name TEXT NOT NULL,
  price_cents INT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id);


-- Historical duplicate version numbers may have skipped these definitions.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS session_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE agent_commissions ADD COLUMN IF NOT EXISTS settled_cents BIGINT NOT NULL DEFAULT 0;
-- Do not infer old per-order allocations. Inconsistent legacy ledgers fail closed.
ALTER TABLE agent_payouts ADD COLUMN IF NOT EXISTS request_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS agent_payout_request_key ON agent_payouts(agent_id,request_key) WHERE request_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS agent_payout_allocations (
 payout_id INT NOT NULL REFERENCES agent_payouts(id) ON DELETE CASCADE,
 commission_id INT NOT NULL REFERENCES agent_commissions(id) ON DELETE CASCADE,
 amount_cents BIGINT NOT NULL CHECK(amount_cents>0),
 PRIMARY KEY(payout_id,commission_id)
);
