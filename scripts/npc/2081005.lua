-- 卡羅賓辨認龍族的變身，准許進入生命之穴。
if npc:map_id() ~= 240040700 or not npc:ask_yes_no("你是誰？要進入生命之穴嗎？") then return end
if not npc:horntail_disguise() then
    npc:say("外人不能進入生命之穴。", false, false)
end
