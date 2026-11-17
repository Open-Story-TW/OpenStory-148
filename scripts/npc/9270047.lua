if npc:map_id() == 551030200 then
    if npc:ask_yes_no("要離開夢幻主題樂園的首領場地嗎？") then
        npc:event_leave()
    end
    return
end
if npc:map_id() ~= 551030100 then
    return
end
if npc:item_count(4032246) < 1 then
    npc:say("你需要 #b#t4032246##k 才能進場。", false, false)
    return
end
if not npc:ask_yes_no("要帶隊挑戰首領嗎？進場後，在想挑戰的雕像前放下一個 #b#t4032246##k。") then
    return
end
local result = npc:event_start("MalaysiaBoss")
if result.ok then
    return
end
local messages = {
    no_party = "請先組隊。",
    not_leader = "請隊長來和我交談。",
    party_size = "需要至少兩名隊員一起來到這裡。",
    level = "參加隊員須達到 90 級。",
    busy = "有隊伍已經在挑戰。",
}
npc:say(messages[result.reason] or "目前無法進場，請稍後再試。", false, false)
