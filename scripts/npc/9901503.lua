-- 混沌闇黑龍王的專用登記。
if npc:map_id() ~= 240050400 or not npc:ask_yes_no("要挑戰混沌闇黑龍王嗎？請讓 6 至 30 名、110 至 200 級的遠征隊員到場。") then return end
local result = npc:event_start("ChaosHorntail")
if not result.ok then
    npc:say("請由混沌闇黑龍王遠征隊隊長申請。全員都要符合條件，且目前須無其他隊伍在挑戰。", false, false)
end
