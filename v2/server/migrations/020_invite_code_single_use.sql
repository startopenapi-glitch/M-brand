-- One invite code = one use, regardless of legacy max_uses.
UPDATE invite_codes SET max_uses=1 WHERE max_uses<>1;
