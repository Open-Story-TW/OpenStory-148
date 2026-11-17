-- 敢死隊的暗號石版，只向持有榮譽隊員象徵的人開放密道。
if npc:map_id() ~= 240050000 then return end
if npc:item_count(4001086) < 1 then
    npc:say("你看不懂石版上的暗號。", false, false)
    return
end
if npc:ask_yes_no("要沿著敢死隊的密道前往闇黑龍王洞穴入口嗎？") then
    npc:warp(240050400, 1)
end
