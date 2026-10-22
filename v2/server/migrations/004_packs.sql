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
ON CONFLICT (pack) DO UPDATE SET
  name = EXCLUDED.name,
  description = EXCLUDED.description,
  price_cents = EXCLUDED.price_cents,
  sort_order = EXCLUDED.sort_order;

-- 新增图片类型索引
CREATE INDEX IF NOT EXISTS idx_job_images_pack ON job_images(job_id, kind);
