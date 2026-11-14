-- A pet is the durable state of one CASH inventory serial. Ownership and the
-- CASH slot stay on asset_cash_items/asset_items; this row never duplicates
-- them. Position, stance and foothold are field-local and never persisted.
CREATE TABLE asset_pets (
    cash_id integer PRIMARY KEY REFERENCES asset_cash_items(id) ON DELETE CASCADE,
    template_id integer NOT NULL CHECK (template_id BETWEEN 5000000 AND 5009999),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 12),
    level smallint NOT NULL DEFAULT 1 CHECK (level BETWEEN 1 AND 30),
    closeness integer NOT NULL DEFAULT 0 CHECK (closeness BETWEEN 0 AND 30000),
    fullness smallint NOT NULL DEFAULT 100 CHECK (fullness BETWEEN 0 AND 100),
    flags integer NOT NULL DEFAULT 0 CHECK (flags BETWEEN 0 AND 65535),
    skill_id integer NOT NULL DEFAULT 0 CHECK (skill_id >= 0),
    -- Native summoned value: 1..3 is the spawn index plus one.
    summoned_by bigint REFERENCES game_characters(id) ON DELETE SET NULL,
    summon_index smallint CHECK (summon_index BETWEEN 0 AND 2),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((summoned_by IS NULL) = (summon_index IS NULL))
);
CREATE UNIQUE INDEX asset_pets_summon_slot ON asset_pets(summoned_by, summon_index) WHERE summoned_by IS NOT NULL;

-- One receipt per authenticated native command, so a replayed PET_FOOD or
-- hunger tick cannot consume food or fullness twice.
CREATE TABLE asset_pet_commands (
    character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    operation_id text NOT NULL CHECK (char_length(operation_id) BETWEEN 16 AND 128),
    kind text NOT NULL CHECK (kind IN ('summon','feed','command','decay','grant')),
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (character_id, operation_id)
);
