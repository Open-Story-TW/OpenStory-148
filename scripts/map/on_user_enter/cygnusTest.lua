-- Source: Legacy MapScriptMethods.cygnusTest (showIntro).
-- The mounted WZ scene owns the transfer to the next map.
local id = map:map_id()
local scene = id == 913040006 and 9 or id - 913040000
map:play_scene("Effect/Direction.img/cygnus/Scene" .. scene)
