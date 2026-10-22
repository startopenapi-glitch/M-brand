-- 2026-10-21: one-time 9.9 unlock for directions + Logo combination card.
-- Historical jobs keep directions_unlocked=true; new paywall jobs start false.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS directions_unlocked BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE invite_codes ADD COLUMN IF NOT EXISTS item_kind TEXT NOT NULL DEFAULT 'directions_unlock';
UPDATE invite_codes SET item_kind='directions_unlock', max_uses=1;
