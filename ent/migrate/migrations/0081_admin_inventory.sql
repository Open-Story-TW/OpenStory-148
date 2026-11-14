-- Every change the web admin makes to a character's bags, mesos or cash
-- wallet. The row is written in the same transaction as the change, so it is
-- also the receipt that makes a repeated request a replay.
CREATE TABLE ops_admin_inventory_ops (
 operation_id text PRIMARY KEY CHECK(length(operation_id) BETWEEN 16 AND 128),
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 actor text NOT NULL CHECK(length(actor) BETWEEN 1 AND 128),
 account_id bigint NOT NULL,
 character_id bigint NOT NULL,
 online boolean NOT NULL,
 kind text NOT NULL CHECK(length(kind) BETWEEN 1 AND 32),
 request jsonb NOT NULL,
 before_state jsonb NOT NULL,
 result jsonb NOT NULL
);
CREATE INDEX ops_admin_inventory_ops_character ON ops_admin_inventory_ops(character_id, at DESC);
CREATE INDEX ops_admin_inventory_ops_account ON ops_admin_inventory_ops(account_id, at DESC);
