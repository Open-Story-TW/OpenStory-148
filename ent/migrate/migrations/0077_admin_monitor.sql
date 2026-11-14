-- History the web admin collects from each world's in-memory event ring.
-- The game services never write here.
CREATE TABLE ops_admin_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 world_id integer NOT NULL,
 instance text NOT NULL,
 seq bigint NOT NULL,
 at timestamptz NOT NULL,
 kind text NOT NULL CHECK(length(kind) BETWEEN 1 AND 32),
 channel integer NOT NULL,
 map_id integer NOT NULL,
 character_id bigint NOT NULL,
 name text NOT NULL,
 body text NOT NULL,
 UNIQUE(world_id,instance,seq)
);
CREATE INDEX ops_admin_events_kind ON ops_admin_events(kind,id);
CREATE INDEX ops_admin_events_at ON ops_admin_events(at);
-- Where the collector stopped reading each world.
CREATE TABLE ops_admin_event_cursors (
 world_id integer PRIMARY KEY,
 instance text NOT NULL,
 seq bigint NOT NULL
);
CREATE TABLE ops_admin_notices (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 at timestamptz NOT NULL DEFAULT clock_timestamp(),
 actor text NOT NULL,
 world_id integer NOT NULL,
 channel integer NOT NULL,
 map_id integer NOT NULL,
 message text NOT NULL,
 delivered integer NOT NULL
);
