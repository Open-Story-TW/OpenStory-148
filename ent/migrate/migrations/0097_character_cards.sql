CREATE UNIQUE INDEX game_character_cards_roster_owner
 ON game_characters (account_id,world_id,id);

CREATE TABLE game_character_cards (
 account_id bigint NOT NULL,
 world_id integer NOT NULL CHECK(world_id BETWEEN 0 AND 127),
 slot integer NOT NULL CHECK(slot BETWEEN 0 AND 5),
 character_id bigint NOT NULL,
 PRIMARY KEY(account_id,world_id,slot),
 UNIQUE(account_id,world_id,character_id),
 FOREIGN KEY(account_id,world_id,character_id)
   REFERENCES game_characters(account_id,world_id,id) ON DELETE CASCADE
);
