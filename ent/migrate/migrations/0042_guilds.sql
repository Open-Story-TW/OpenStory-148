-- Guild membership, titles, emblem and notice are durable. Online state and
-- channel are presence, never persisted. Skills, GP and alliances are later.
CREATE TABLE game_guilds (
    id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    world_id integer NOT NULL CHECK (world_id BETWEEN 0 AND 127),
    name text NOT NULL CHECK (char_length(name) BETWEEN 3 AND 12),
    leader_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    titles text[] NOT NULL DEFAULT ARRAY['公會長','副會長','成員','成員','成員'] CHECK (cardinality(titles) = 5),
    capacity integer NOT NULL DEFAULT 10 CHECK (capacity BETWEEN 1 AND 200),
    logo_bg integer NOT NULL DEFAULT 0 CHECK (logo_bg BETWEEN 0 AND 65535),
    logo_bg_color integer NOT NULL DEFAULT 0 CHECK (logo_bg_color BETWEEN 0 AND 255),
    logo integer NOT NULL DEFAULT 0 CHECK (logo BETWEEN 0 AND 65535),
    logo_color integer NOT NULL DEFAULT 0 CHECK (logo_color BETWEEN 0 AND 255),
    notice text NOT NULL DEFAULT '' CHECK (char_length(notice) <= 100),
    gp integer NOT NULL DEFAULT 0 CHECK (gp >= 0),
    level integer NOT NULL DEFAULT 1 CHECK (level BETWEEN 1 AND 30),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX game_guilds_world_name ON game_guilds(world_id, lower(name));

CREATE TABLE game_guild_members (
    character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
    guild_id integer NOT NULL REFERENCES game_guilds(id) ON DELETE CASCADE,
    rank smallint NOT NULL CHECK (rank BETWEEN 1 AND 5),
    contribution integer NOT NULL DEFAULT 0 CHECK (contribution >= 0),
    joined_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX game_guild_members_guild ON game_guild_members(guild_id, joined_at);
CREATE UNIQUE INDEX game_guild_single_master ON game_guild_members(guild_id) WHERE rank = 1;

-- Legacy keeps one pending invitation per target name for 20 minutes.
CREATE TABLE game_guild_invitations (
    target_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
    guild_id integer NOT NULL REFERENCES game_guilds(id) ON DELETE CASCADE,
    inviter_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    CHECK (target_id <> inviter_id)
);

CREATE TABLE game_guild_operations (
    character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
    operation_id text NOT NULL CHECK (char_length(operation_id) BETWEEN 16 AND 128),
    action text NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (character_id, operation_id)
);
