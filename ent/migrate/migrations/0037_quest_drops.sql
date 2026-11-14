ALTER TABLE asset_item_drops
 ADD COLUMN quest_id integer NOT NULL DEFAULT 0 CHECK(quest_id BETWEEN 0 AND 65535),
 ADD COLUMN quest_drop_type integer NOT NULL DEFAULT 2 CHECK(quest_drop_type BETWEEN 0 AND 3),
 ADD COLUMN quest_party_id bigint NOT NULL DEFAULT 0 CHECK(quest_party_id>=0),
 ADD COLUMN quest_owner_until timestamptz;
ALTER TABLE asset_item_drops ADD CONSTRAINT asset_item_drops_quest_scope CHECK(
 (quest_id=0 AND quest_drop_type=2 AND quest_party_id=0 AND quest_owner_until IS NULL)
 OR (quest_id>0 AND mesos=0 AND inv_type BETWEEN 2 AND 4 AND equipment IS NULL
     AND ((quest_drop_type IN(0,1) AND quest_owner_until IS NOT NULL)
       OR (quest_drop_type IN(2,3) AND quest_owner_until IS NULL)))
);
