-- Guild skills (Skill.wz 9100), GP contribution receipts and guild alliances.
CREATE TABLE game_guild_skills (
    guild_id integer NOT NULL REFERENCES game_guilds(id) ON DELETE CASCADE,
    skill_id integer NOT NULL CHECK (skill_id BETWEEN 91000000 AND 91009999),
    level smallint NOT NULL CHECK (level BETWEEN 1 AND 30),
    -- Unix milliseconds; -1 is a permanent skill (no WZ period).
    expires_at bigint NOT NULL,
    purchaser text NOT NULL,
    activator text NOT NULL,
    PRIMARY KEY (guild_id, skill_id)
);

-- One GP grant per (character, period) so hourly presence is idempotent.
CREATE TABLE game_guild_gp_grants (
    character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    period text NOT NULL CHECK (char_length(period) BETWEEN 1 AND 64),
    guild_id integer NOT NULL REFERENCES game_guilds(id) ON DELETE CASCADE,
    amount integer NOT NULL CHECK (amount > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (character_id, period)
);

CREATE TABLE game_alliances (
    id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    world_id integer NOT NULL CHECK (world_id BETWEEN 0 AND 127),
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 12),
    leader_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    titles text[] NOT NULL DEFAULT ARRAY['Master','Jr.Master','Member','Member','Member'] CHECK (cardinality(titles) = 5),
    capacity integer NOT NULL DEFAULT 2 CHECK (capacity BETWEEN 2 AND 5),
    notice text NOT NULL DEFAULT '' CHECK (char_length(notice) <= 100),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX game_alliances_world_name ON game_alliances(world_id, lower(name));

ALTER TABLE game_guilds ADD COLUMN alliance_id integer REFERENCES game_alliances(id) ON DELETE SET NULL;
ALTER TABLE game_guilds ADD COLUMN alliance_joined_at timestamptz;
CREATE INDEX game_guilds_alliance ON game_guilds(alliance_id) WHERE alliance_id IS NOT NULL;
-- Legacy alliance rank: 1 alliance master, 2 other guild masters, 3..5 members.
ALTER TABLE game_guild_members ADD COLUMN alliance_rank smallint NOT NULL DEFAULT 5 CHECK (alliance_rank BETWEEN 1 AND 5);

-- One pending alliance invitation per invited guild.
CREATE TABLE game_alliance_invitations (
    guild_id integer PRIMARY KEY REFERENCES game_guilds(id) ON DELETE CASCADE,
    alliance_id integer NOT NULL REFERENCES game_alliances(id) ON DELETE CASCADE,
    inviter_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);
