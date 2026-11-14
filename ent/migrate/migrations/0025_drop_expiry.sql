-- Legacy MapleMap registers ordinary item/meso drops for 120,000 ms.
-- Retain expired rows for audit and prevent a replay from resurrecting them.
ALTER TABLE asset_item_drops
 ADD COLUMN expires_at timestamptz,
 ADD COLUMN expired_at timestamptz;
UPDATE asset_item_drops SET expires_at=created_at+interval '120 seconds';
ALTER TABLE asset_item_drops
 ALTER COLUMN expires_at SET NOT NULL,
 ALTER COLUMN expires_at SET DEFAULT (clock_timestamp()+interval '120 seconds'),
 ADD CONSTRAINT asset_item_drop_one_terminal_state
 CHECK (expired_at IS NULL OR picked_up_by IS NULL);
CREATE INDEX asset_item_drops_expiry
 ON asset_item_drops(world_id,expires_at,object_id)
 WHERE picked_up_by IS NULL AND expired_at IS NULL;
