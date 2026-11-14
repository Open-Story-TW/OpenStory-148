-- Source: Legacy MapScriptMethods.aranDirection (showIntro) and scripts/map/onUserEnter/aranDirection.js.
-- The mounted WZ scene owns the transfer to the next map.
local id = map:map_id()
local gender = map:gender() == 0 and 0 or 1
local scenes = {
    [914090010] = "Scene0",
    [914090011] = "Scene1" .. gender,
    [914090012] = "Scene2" .. gender,
    [914090013] = "Scene3",
    [914090100] = "HandedPoleArm" .. gender,
    [914090200] = "Maha",
}
if scenes[id] then map:play_scene("Effect/Direction1.img/aranTutorial/" .. scenes[id]) end
