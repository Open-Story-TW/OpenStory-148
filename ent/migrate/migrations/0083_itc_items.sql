-- Item trading center (拍賣) listings. A row owns its item from the moment
-- it leaves the seller's inventory until someone takes it back into one:
--   selling  on the 販售 list; owner_id is the seller
--   storage  in owner_id's 拍賣倉庫 (bought, cancelled or expired)
CREATE TABLE asset_itc_items (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 seller_id bigint NOT NULL REFERENCES game_characters(id),
 seller_account_id bigint NOT NULL REFERENCES auth_accounts(id),
 seller_name text NOT NULL,
 owner_id bigint NOT NULL REFERENCES game_characters(id),
 state text NOT NULL CHECK(state IN('selling','storage')),
 inv_type smallint NOT NULL CHECK(inv_type BETWEEN 1 AND 4),
 template_id integer NOT NULL CHECK(template_id>0),
 quantity integer NOT NULL CHECK(quantity BETWEEN 1 AND 30000),
 equipment jsonb,
 item_expires_at bigint NOT NULL DEFAULT 0,
 price bigint NOT NULL CHECK(price BETWEEN 1 AND 2147483647),
 listed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 ends_at timestamptz NOT NULL,
 buyer_id bigint,
 revision bigint NOT NULL DEFAULT 1
);
CREATE INDEX asset_itc_items_selling ON asset_itc_items(world_id,state,inv_type,id);
CREATE INDEX asset_itc_items_owner ON asset_itc_items(owner_id,state,id);
CREATE TABLE asset_itc_cart (
 character_id bigint NOT NULL REFERENCES game_characters(id),
 item_id bigint NOT NULL REFERENCES asset_itc_items(id) ON DELETE CASCADE,
 added_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(character_id,item_id)
);
-- One row per trading center command, so a repeated request is a replay.
CREATE TABLE ops_itc_operations (
 operation_id text PRIMARY KEY CHECK(length(operation_id) BETWEEN 16 AND 128),
 character_id bigint NOT NULL,
 kind text NOT NULL,
 item_id bigint NOT NULL,
 at timestamptz NOT NULL DEFAULT clock_timestamp()
);
