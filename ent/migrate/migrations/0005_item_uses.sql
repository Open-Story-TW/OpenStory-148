-- Keep the receipt after the last item in a stack is deleted. Requests are
-- unique within one authenticated character session, including across retries.
CREATE TABLE asset_item_uses (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 session_id text NOT NULL,
 generation bigint NOT NULL CHECK(generation > 0),
 client_tick bigint NOT NULL CHECK(client_tick BETWEEN 0 AND 4294967295),
 item_instance_id bigint NOT NULL,
 template_id integer NOT NULL,
 slot smallint NOT NULL CHECK(slot BETWEEN 1 AND 128),
 quantity_after integer NOT NULL CHECK(quantity_after BETWEEN 0 AND 29999),
 hp_after integer NOT NULL CHECK(hp_after > 0),
 character_revision bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id, session_id, generation, client_tick)
);
