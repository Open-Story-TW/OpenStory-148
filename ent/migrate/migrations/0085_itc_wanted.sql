-- Buy requests (購買登記) on the item trading center. A request is a row of
-- kind 'wanted' owned by the buyer (seller_id/seller_name name the buyer);
-- held_points is what the buyer paid in advance, returned when the request
-- is cancelled or runs out.
ALTER TABLE asset_itc_items DROP CONSTRAINT asset_itc_items_kind_check;
ALTER TABLE asset_itc_items ADD CONSTRAINT asset_itc_items_kind_check CHECK(kind IN('sale','auction','wanted'));
ALTER TABLE asset_itc_items ADD COLUMN comment text NOT NULL DEFAULT '' CHECK(length(comment)<=64);
