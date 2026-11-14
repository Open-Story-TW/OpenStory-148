-- Auctions (競標) on the item trading center. price is the starting price;
-- current_price is the highest bid (the starting price before any bid).
-- held_points is what the high bidder paid in advance (bid plus commission);
-- it goes back to them when someone outbids them.
ALTER TABLE asset_itc_items ADD COLUMN kind text NOT NULL DEFAULT 'sale' CHECK(kind IN('sale','auction'));
ALTER TABLE asset_itc_items ADD COLUMN buy_now_price bigint NOT NULL DEFAULT 0 CHECK(buy_now_price BETWEEN 0 AND 2147483647);
ALTER TABLE asset_itc_items ADD COLUMN bid_step integer NOT NULL DEFAULT 0 CHECK(bid_step>=0);
ALTER TABLE asset_itc_items ADD COLUMN bid_count integer NOT NULL DEFAULT 0 CHECK(bid_count>=0);
ALTER TABLE asset_itc_items ADD COLUMN current_price bigint NOT NULL DEFAULT 0 CHECK(current_price BETWEEN 0 AND 2147483647);
ALTER TABLE asset_itc_items ADD COLUMN high_bidder_id bigint REFERENCES game_characters(id);
ALTER TABLE asset_itc_items ADD COLUMN high_bidder_account_id bigint REFERENCES auth_accounts(id);
ALTER TABLE asset_itc_items ADD COLUMN held_points bigint NOT NULL DEFAULT 0 CHECK(held_points>=0);
CREATE INDEX asset_itc_items_kind ON asset_itc_items(world_id,kind,state,id);
