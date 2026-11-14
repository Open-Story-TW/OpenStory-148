-- All-job opening (S1). subcategory is the Explorer sub job sent at creation
-- (1 Dual Blade, 2 Cannoneer) and written by CharacterStat. sp_books parks
-- unspent SP of non-current books for the client's extended-SP jobs
-- (Resistance/Demon, Evan, Mercedes); the live book stays in sp.
-- demon_mark is the Demon Slayer face accessory the client reads after the
-- subcategory and at the end of AvatarLook (is_no_mana_job, 0x45012C).
ALTER TABLE game_characters ADD COLUMN subcategory smallint NOT NULL DEFAULT 0 CHECK(subcategory BETWEEN 0 AND 2);
ALTER TABLE game_characters ADD COLUMN sp_books integer[] NOT NULL DEFAULT '{0,0,0,0,0,0,0,0,0,0}'
 CHECK(cardinality(sp_books)=10 AND 0<=ALL(sp_books) AND 255>=ALL(sp_books));
ALTER TABLE game_characters ADD COLUMN demon_mark integer NOT NULL DEFAULT 0 CHECK(demon_mark=0 OR demon_mark BETWEEN 1012000 AND 1012999);
