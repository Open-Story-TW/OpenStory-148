-- S0b: Steal (4201004 family) takes at most one item per monster spawn.
CREATE TABLE asset_mob_steals (
 spawn_operation_id text PRIMARY KEY CHECK(length(spawn_operation_id) BETWEEN 16 AND 200),
 operation_id text NOT NULL UNIQUE CHECK(length(operation_id) BETWEEN 16 AND 200),
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 skill_id integer NOT NULL CHECK(skill_id > 0),
 mob_id integer NOT NULL CHECK(mob_id > 0),
 template_id integer NOT NULL CHECK(template_id >= 0),
 drop_object_id integer,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
