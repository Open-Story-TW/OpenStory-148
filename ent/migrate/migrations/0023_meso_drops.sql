ALTER TABLE asset_item_drops ADD COLUMN mesos bigint NOT NULL DEFAULT 0 CHECK(mesos BETWEEN 0 AND 2147483647);
ALTER TABLE asset_item_drops DROP CONSTRAINT asset_item_drops_inv_type_check;
ALTER TABLE asset_item_drops DROP CONSTRAINT asset_item_drops_template_id_check;
ALTER TABLE asset_item_drops ADD CHECK(inv_type BETWEEN 0 AND 4);
ALTER TABLE asset_item_drops ADD CHECK(template_id >= 0);
ALTER TABLE asset_item_drops ADD CHECK(
 (mesos=0 AND inv_type BETWEEN 1 AND 4 AND template_id>0)
 OR (mesos>0 AND inv_type=0 AND template_id=0 AND quantity=1 AND equipment IS NULL)
);
