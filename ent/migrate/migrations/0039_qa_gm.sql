-- Dedicated, expiring QA capability; no existing account or character is promoted.
-- The initial P2 schema permitted only ordinary characters. Broaden solely to
-- the reviewed QA rank, with all live authorization still requiring its grant.
ALTER TABLE game_characters DROP CONSTRAINT game_characters_gm_check;
ALTER TABLE game_characters ADD CONSTRAINT game_characters_gm_check CHECK(gm IN(0,6));
CREATE TABLE ops_qa_gm_grants (
 character_id bigint PRIMARY KEY REFERENCES game_characters(id) ON DELETE CASCADE,
 account_id bigint NOT NULL REFERENCES auth_accounts(id) ON DELETE CASCADE,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 role text NOT NULL CHECK(role='qa_skill_tester'),
 expires_at timestamptz NOT NULL,
 enabled boolean NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 authorized_by text NOT NULL CHECK(length(authorized_by) BETWEEN 1 AND 128),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 512),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE ops_qa_gm_authorizations (
 operation_id text PRIMARY KEY CHECK(length(operation_id) BETWEEN 16 AND 128),
 request_hash text NOT NULL CHECK(length(request_hash)=64),
 actor text NOT NULL,
 action text NOT NULL CHECK(action IN('grant','revoke')),
 account_id bigint NOT NULL,
 character_id bigint NOT NULL,
 world_id integer NOT NULL,
 reason text NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- Audit rows intentionally retain numeric target identities after QA deletion.
CREATE TABLE ops_qa_gm_commands (
 character_id bigint NOT NULL,
 operation_id text NOT NULL CHECK(length(operation_id) BETWEEN 16 AND 128),
 account_id bigint NOT NULL,
 world_id integer NOT NULL,
 session_id text NOT NULL,
 generation bigint NOT NULL,
 request_hash text NOT NULL CHECK(length(request_hash)=64),
 command text NOT NULL,
 args jsonb NOT NULL,
 grant_revision bigint NOT NULL,
 outcome text NOT NULL CHECK(outcome IN('accepted','denied')),
 before_state jsonb NOT NULL,
 after_state jsonb NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(character_id,operation_id)
);
