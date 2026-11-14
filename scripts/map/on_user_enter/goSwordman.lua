-- Source: Legacy MapScriptMethods.goSwordman (showIntro) and scripts/map/onUserEnter/goSwordman.js.
-- The mounted WZ scene owns the transfer to the next map.
local gender = map:gender() == 0 and 0 or 1
map:play_scene("Effect/Direction3.img/swordman/Scene" .. gender)
