-- 2026-10-21: 17 项单项合计 ¥149.00，全选封顶 ¥99.00。
-- 只调整未来下单价格；历史 orders/order_items 快照不追溯。
UPDATE item_pricing SET price_cents = CASE kind
  WHEN 'logo_h'       THEN 1900
  WHEN 'logo_v'       THEN 1900
  WHEN 'logo_icon'    THEN 1900
  WHEN 'logo_mono'    THEN 900
  WHEN 'logo_white'   THEN 900
  WHEN 'color_typo'   THEN 1200
  WHEN 'photo_style'  THEN 800
  WHEN 'worldview'    THEN 1200
  WHEN 'logo_logic'   THEN 1000
  WHEN 'app_overview' THEN 1000
  WHEN 'card_front'   THEN 500
  WHEN 'card_back'    THEN 300
  WHEN 'letterhead'   THEN 400
  WHEN 'wx_avatar'    THEN 200
  WHEN 'wx_cover'     THEN 300
  WHEN 'ppt_cover'    THEN 300
  WHEN 'brand_story'  THEN 200
  ELSE price_cents
END
WHERE kind IN (
  'logo_h','logo_v','logo_icon','logo_mono','logo_white',
  'color_typo','photo_style','worldview','logo_logic','app_overview',
  'card_front','card_back','letterhead','wx_avatar','wx_cover','ppt_cover','brand_story'
);
