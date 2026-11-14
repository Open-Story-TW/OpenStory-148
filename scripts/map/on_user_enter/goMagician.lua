-- Source: Legacy MapScriptMethods.goMagician (showIntro) and scripts/map/onUserEnter/goMagician.js.
-- The mounted WZ scene owns the transfer to the next map.
local gender = map:gender() == 0 and 0 or 1
map:play_scene("Effect/Direction3.img/magician/Scene" .. gender)
