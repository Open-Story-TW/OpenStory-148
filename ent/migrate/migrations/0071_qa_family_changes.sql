-- Offline QA family switch (S1): Mercedes/Demon/Phantom cannot be created by
-- the v145 client. Only characters holding an active QA grant qualify.
CREATE TABLE ops_qa_family_changes (
 operation_id text PRIMARY KEY CHECK(length(operation_id) BETWEEN 16 AND 128),
 character_id bigint NOT NULL,
 account_id bigint NOT NULL,
 actor text NOT NULL CHECK(length(actor) BETWEEN 1 AND 128),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 512),
 job integer NOT NULL,
 grant_revision bigint NOT NULL,
 before_state jsonb NOT NULL,
 after_state jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
