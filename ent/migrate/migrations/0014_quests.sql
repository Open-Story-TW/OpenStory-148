CREATE TABLE game_quests (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 quest_id integer NOT NULL CHECK(quest_id BETWEEN 1 AND 65535),
 state smallint NOT NULL CHECK(state BETWEEN 0 AND 2),
 started_at timestamptz,
 completed_at timestamptz,
 mob_kills jsonb NOT NULL DEFAULT '{}',
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 PRIMARY KEY(character_id,quest_id)
);
CREATE TABLE asset_quest_commands (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 operation_id text NOT NULL,
 quest_id integer NOT NULL,
 action text NOT NULL CHECK(action IN('start','complete','forfeit')),
 npc_id integer NOT NULL,
 script text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,operation_id)
);
CREATE TABLE game_quest_kill_receipts (
 character_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 operation_id text NOT NULL,
 mob_id integer NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(character_id,operation_id)
);
