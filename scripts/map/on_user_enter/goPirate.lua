-- Source: Legacy MapScriptMethods.goPirate (showIntro) and scripts/map/onUserEnter/goPirate.js.
-- The mounted WZ scene owns the transfer to the next map.
local gender = map:gender() == 0 and 0 or 1
map:play_scene("Effect/Direction3.img/pirate/Scene" .. gender)
