CREATE TABLE asset_physical_attacks (
    character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    operation_id text NOT NULL,
    session_id text NOT NULL,
    generation bigint NOT NULL,
    client_tick bigint NOT NULL CHECK (client_tick BETWEEN 0 AND 4294967295),
    skill_id integer NOT NULL CHECK (skill_id IN (1001004,1001005)),
    payload_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(character_id,operation_id),
    UNIQUE(character_id,session_id,generation,client_tick)
);
