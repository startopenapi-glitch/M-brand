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
ON CONFLICT (kind) DO UPDATE SET name=EXCLUDED.name, category=EXCLUDED.category, price_cents=EXCLUDED.price_cents;

CREATE TABLE IF NOT EXISTS order_items (
  id SERIAL PRIMARY KEY,
  order_id TEXT NOT NULL,
  item_kind TEXT NOT NULL,
  item_name TEXT NOT NULL,
  price_cents INT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id);
