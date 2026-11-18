-- 考驗之門在苦難者之房。
local story = npc:quest_state(3863)
local spark = npc:quest_state(3864)
if spark == 2 or story < 2 and npc:quest_state(3862) == 2 then
    if npc:ask_yes_no("要前往六手邪神的祭壇入口嗎？") then
        npc:warp(252030000, 3)
    end
else
    npc:say("要通過這道門，必須帶來太陽火花。", false, false)
end
