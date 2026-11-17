-- Legacy Shammos entry and ambush, with TWMS v148 Quest.wz/1209 requirements.
if npc:map_id() == 921120005 then
    if not npc:hoblin_ambush() then
        npc:say("清除埋伏的怪物後，再請隊長來找我。", false, false)
    end
elseif npc:map_id() == 921120000 then
    local option = npc:menu("要阻止侏儒帝王復活嗎？請集合 3 至 6 名 120 級以上的隊員。", {
        {id = 0, text = "進入冰谷"},
    })
    if option == 0 then
        local entry = npc:event_start("Rex")
        if entry.ok then return end
        local reasons = {
            no_party = "請先組隊。",
            not_leader = "請讓隊長跟我說話。",
            party_size = "需要 3 至 6 名隊員同時在場。",
            level = "所有隊員都需要達到 120 級。",
            busy = "另一隊正在挑戰，請稍後再來。",
        }
        npc:say(reasons[entry.reason] or "現在無法進入冰谷。", false, false)
    end
end
