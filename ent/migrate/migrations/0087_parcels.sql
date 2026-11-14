-- 宅配 (Duey, NPC 9010009). A parcel is what one character sent another:
-- mesos, at most one item, or both. It is in transit until arrives_at (the
-- parcel_delay_minutes setting; a quick parcel arrives at once), then waits
-- until the receiver takes it, deletes it, or it passes expires_at (thirty
-- days after arrival), when opening the box removes it and lists it once as
-- expired.
CREATE TABLE asset_parcels (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK(id <= 2147483647),
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 receiver_id bigint NOT NULL REFERENCES game_characters(id) ON DELETE CASCADE,
 -- The sender only labels the parcel; it may be deleted meanwhile.
 sender_id bigint NOT NULL,
 sender_account_id bigint NOT NULL,
 sender_name text NOT NULL,
 -- The parcel record carries the mesos as a 32-bit field.
 mesos bigint NOT NULL DEFAULT 0 CHECK(mesos BETWEEN 0 AND 2147483647),
 -- 0 = no item. Cash items are not sent by parcel.
 inv_type smallint NOT NULL DEFAULT 0 CHECK(inv_type BETWEEN 0 AND 4),
 template_id integer NOT NULL DEFAULT 0,
 quantity integer NOT NULL DEFAULT 0 CHECK(quantity BETWEEN 0 AND 30000),
 -- Equipment keeps its identity and stats, as in storage.
 equipment jsonb,
 quick boolean NOT NULL DEFAULT false,
 message text NOT NULL DEFAULT '',
 sent_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 arrives_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 CHECK(arrives_at>=sent_at AND expires_at>arrives_at),
 CHECK(mesos>0 OR inv_type>0),
 CHECK((inv_type=0)=(template_id=0) AND (inv_type=0)=(quantity=0)),
 CHECK((inv_type=1)=(equipment IS NOT NULL))
);
CREATE INDEX asset_parcels_receiver ON asset_parcels(receiver_id,id);
