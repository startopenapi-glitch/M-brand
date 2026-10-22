-- Invite codes now represent one logo_icon entitlement, not a legacy board pack.
-- Existing jobs keep their historical rights via legacy_code_rights; new jobs are
-- inserted with false and read the code's item_kind.
ALTER TABLE invite_codes ADD COLUMN IF NOT EXISTS item_kind TEXT NOT NULL DEFAULT 'logo_icon';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS legacy_code_rights BOOLEAN NOT NULL DEFAULT true;
