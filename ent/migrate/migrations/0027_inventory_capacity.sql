CREATE TABLE asset_inventory_capacity (
    character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    inv_type smallint NOT NULL CHECK (inv_type BETWEEN 1 AND 5),
    capacity smallint NOT NULL CHECK (capacity BETWEEN 24 AND 96),
    PRIMARY KEY (character_id,inv_type)
);

-- Existing characters keep 24 slots through the absence-of-row default.
-- Extensions are committed inside the same character-fenced reward transaction.
