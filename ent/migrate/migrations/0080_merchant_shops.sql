-- Hired merchants. A shop row owns its items and its income, so the shop
-- survives the owner logging out and a world restart. state:
--   setup        owner is stocking a shop that has never opened
--   open         visible on the map, visitors may buy
--   maintenance  owner is inside, visitors are locked out
--   closed       off the map; items and income wait at Fredrick
CREATE TABLE asset_merchant_shops (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 character_id bigint NOT NULL REFERENCES game_characters(id),
 account_id bigint NOT NULL REFERENCES auth_accounts(id),
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 channel_id integer NOT NULL CHECK(channel_id>=1),
 map_id integer NOT NULL,
 x integer NOT NULL,
 y integer NOT NULL,
 foothold integer NOT NULL DEFAULT 0,
 permit_item_id integer NOT NULL CHECK(permit_item_id/10000=503),
 owner_name text NOT NULL,
 title text NOT NULL CHECK(length(title) BETWEEN 1 AND 64),
 state text NOT NULL CHECK(state IN('setup','open','maintenance','closed')),
 -- Income is a balance, so it is 64-bit.
 mesos bigint NOT NULL DEFAULT 0 CHECK(mesos>=0),
 revision bigint NOT NULL DEFAULT 1,
 opened_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 closed_at timestamptz,
 -- Legacy allows one merchant (or one Fredrick package) per account.
 UNIQUE(account_id,world_id),
 UNIQUE(character_id)
);
CREATE INDEX asset_merchant_shops_world ON asset_merchant_shops(world_id,state);
CREATE TABLE asset_merchant_items (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 shop_id bigint NOT NULL REFERENCES asset_merchant_shops(id) ON DELETE CASCADE,
 position integer NOT NULL CHECK(position BETWEEN 1 AND 64),
 inv_type smallint NOT NULL CHECK(inv_type BETWEEN 1 AND 5),
 template_id integer NOT NULL CHECK(template_id>0),
 bundles integer NOT NULL CHECK(bundles BETWEEN 0 AND 30000),
 per_bundle integer NOT NULL CHECK(per_bundle BETWEEN 1 AND 30000),
 -- 32-bit unless the long money contract is on; the service enforces it.
 price bigint NOT NULL CHECK(price>=1),
 equipment jsonb,
 -- Serial of a cash item on sale (asset_cash_items.id, state 'merchant').
 cash_id bigint NOT NULL DEFAULT 0,
 expires_at bigint NOT NULL DEFAULT 0,
 UNIQUE(shop_id,position) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE asset_merchant_sales (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 shop_id bigint NOT NULL REFERENCES asset_merchant_shops(id) ON DELETE CASCADE,
 template_id integer NOT NULL,
 bundles integer NOT NULL,
 per_bundle integer NOT NULL,
 total bigint NOT NULL,
 buyer_id bigint NOT NULL,
 buyer_name text NOT NULL,
 sold_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX asset_merchant_sales_shop ON asset_merchant_sales(shop_id,id);
-- A cash item on sale belongs to no character until it is bought or taken back.
ALTER TABLE asset_cash_items DROP CONSTRAINT asset_cash_items_state_check;
ALTER TABLE asset_cash_items ADD CONSTRAINT asset_cash_items_state_check CHECK(state IN('locker','inventory','storage','expired','merchant'));
