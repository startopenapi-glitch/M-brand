-- Persist exact Native request parameters for same-order uncertain-response recovery.
-- No credentials are stored; description, merchant identities, amount and expiry only.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS checkout_request TEXT NOT NULL DEFAULT '';
-- A fulfilled historical four-board ticket must never be returned by a later add-on failure.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS legacy_fulfilled BOOLEAN NOT NULL DEFAULT false;
UPDATE jobs j SET legacy_fulfilled=true
WHERE COALESCE(j.quota_code,'')<>'' AND NOT j.refunded AND
 (j.stage='done' OR (SELECT COUNT(DISTINCT i.idx) FROM job_images i WHERE i.job_id=j.id AND i.kind='board' AND i.idx BETWEEN 1 AND 4 AND COALESCE(i.file,'')<>'')=4);
