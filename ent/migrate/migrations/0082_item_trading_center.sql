-- A ticket to the cash shop process says which stage the client asked for:
-- the cash shop, or the item trading center (拍賣) the same process serves.
ALTER TABLE ops_service_transfers ADD COLUMN stage text NOT NULL DEFAULT 'cashshop' CHECK(stage IN('cashshop','itc'));
