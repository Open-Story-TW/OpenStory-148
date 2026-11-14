-- Source: Legacy MapScriptMethods.evanPromotion (showIntro).
-- The mounted WZ scene owns the transfer to the next map.
local id = map:map_id()
local gender = map:gender() == 0 and 0 or 1
local scenes = {
    [900090000] = "Scene0" .. gender,
    [900090001] = "Scene1",
    [900090002] = "Scene2" .. gender,
    [900090003] = "Scene3",
}
if scenes[id] then map:play_scene("Effect/Direction4.img/promotion/" .. scenes[id]) end
