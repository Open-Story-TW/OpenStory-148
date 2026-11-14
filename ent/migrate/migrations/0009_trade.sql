CREATE TABLE asset_trade_settlements (
 trade_id text PRIMARY KEY,
 first_character_id bigint NOT NULL REFERENCES game_characters(id),
 second_character_id bigint NOT NULL REFERENCES game_characters(id),
 request_hash text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK(first_character_id <> second_character_id)
);
CREATE INDEX asset_trade_first ON asset_trade_settlements(first_character_id);
CREATE INDEX asset_trade_second ON asset_trade_settlements(second_character_id);
