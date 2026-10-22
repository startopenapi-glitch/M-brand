-- Requeue only recorded paid projects missing at least one selected artifact.
-- Worker reads the paid order/item snapshots; no new payment or commission is created.
UPDATE jobs j SET status='queued',stage='boards',error='',delivery_version=delivery_version+1
WHERE j.status IN ('done','error') AND j.chosen BETWEEN 0 AND 2
AND EXISTS (SELECT 1 FROM orders o WHERE o.job_id=j.id AND o.status='paid' AND
 (EXISTS(SELECT 1 FROM order_items oi WHERE oi.order_id=o.id AND NOT EXISTS(SELECT 1 FROM job_images i WHERE i.job_id=j.id AND i.kind=oi.item_kind))
 OR (o.pack IN ('all','logo','system','boards','apps') AND NOT EXISTS(SELECT 1 FROM order_items oi WHERE oi.order_id=o.id)
 AND NOT o.pack=ANY(COALESCE(j.unlocked_packs,ARRAY[]::text[]))))) ;
