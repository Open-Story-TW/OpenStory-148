-- An item sold to a buy request (申請賣出) first waits for the buyer under
-- 我的拍賣 / 等待購買 as a row of kind 'offer' that names its request; the
-- buyer's 購買 completes the trade and 取消 sends it back to the seller.
ALTER TABLE asset_itc_items DROP CONSTRAINT asset_itc_items_kind_check;
ALTER TABLE asset_itc_items ADD CONSTRAINT asset_itc_items_kind_check CHECK(kind IN('sale','auction','wanted','offer'));
ALTER TABLE asset_itc_items ADD COLUMN request_id bigint;
CREATE INDEX asset_itc_items_request ON asset_itc_items(request_id) WHERE request_id IS NOT NULL;
-- 我的拍賣 / 交易紀錄: one row per side of every finished trade.
CREATE TABLE asset_itc_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 world_id integer NOT NULL,
 side text NOT NULL CHECK(side IN('bought','sold')),
 how text NOT NULL CHECK(how IN('sale','auction','request')),
 inv_type smallint NOT NULL,
 template_id integer NOT NULL,
 quantity integer NOT NULL,
 price bigint NOT NULL,
 counterpart text NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX asset_itc_history_character ON asset_itc_history(character_id,id DESC);
