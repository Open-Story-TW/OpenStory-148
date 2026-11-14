-- Source: Legacy MapScriptMethods.meetWithDragon (showIntro).
-- The mounted WZ scene owns the transfer to the next map.
local gender = map:gender() == 0 and 0 or 1
map:play_scene("Effect/Direction4.img/meetWithDragon/Scene" .. gender)
