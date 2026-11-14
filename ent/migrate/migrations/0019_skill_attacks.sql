CREATE TABLE asset_skill_attacks (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 operation_id text NOT NULL,
 spawn_operation_id text NOT NULL,
 session_id text NOT NULL,
 generation bigint NOT NULL,
 client_tick bigint NOT NULL CHECK(client_tick BETWEEN 0 AND 4294967295),
 skill_id integer NOT NULL,
 target_object_id integer NOT NULL,
 target_mob_id integer NOT NULL,
 damage integer NOT NULL CHECK(damage>=0),
 defeated boolean NOT NULL,
 experience bigint NOT NULL CHECK(experience>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,operation_id),
 UNIQUE(character_id,session_id,generation,client_tick)
);
