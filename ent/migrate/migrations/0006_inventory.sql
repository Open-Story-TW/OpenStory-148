-- Slot swaps commit atomically without an intermediate uniqueness violation.
ALTER TABLE asset_equipment DROP CONSTRAINT asset_equipment_character_id_slot_key;
ALTER TABLE asset_equipment ADD CONSTRAINT asset_equipment_character_id_slot_key UNIQUE(character_id,slot) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE asset_items DROP CONSTRAINT asset_items_character_id_inv_type_slot_key;
ALTER TABLE asset_items ADD CONSTRAINT asset_items_character_id_inv_type_slot_key UNIQUE(character_id,inv_type,slot) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE asset_inventory_commands (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 session_id text NOT NULL,
 generation bigint NOT NULL CHECK(generation > 0),
 client_tick bigint NOT NULL CHECK(client_tick BETWEEN 0 AND 4294967295),
 inv_type smallint NOT NULL,
 source_slot smallint NOT NULL,
 destination_slot smallint NOT NULL,
 quantity integer NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,session_id,generation,client_tick)
);

-- Persisted item object IDs are deliberately outside the meso object's range.
CREATE TABLE asset_item_drops (
 object_id integer GENERATED ALWAYS AS IDENTITY (START WITH 1000000000 MINVALUE 1000000000 MAXVALUE 2147483647) PRIMARY KEY,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 channel_id integer NOT NULL CHECK(channel_id BETWEEN 1 AND 40),
 map_id integer NOT NULL CHECK(map_id >= 0),
 owner_id bigint NOT NULL,
 x integer NOT NULL,
 y integer NOT NULL,
 inv_type smallint NOT NULL CHECK(inv_type BETWEEN 1 AND 4),
 template_id integer NOT NULL CHECK(template_id > 0),
 quantity integer NOT NULL CHECK(quantity BETWEEN 1 AND 30000),
 equipment jsonb,
 picked_up_by bigint,
 pickup_result jsonb,
 created_at timestamptz NOT NULL DEFAULT now(),
 picked_up_at timestamptz,
 CHECK ((picked_up_by IS NULL) = (pickup_result IS NULL))
);
CREATE INDEX asset_item_drops_room ON asset_item_drops(world_id,channel_id,map_id) WHERE picked_up_by IS NULL;
