local choice = npc:menu("結婚組隊任務需要六位 40 級以上的已婚隊員。", {
    { id = 0, text = "離開挑戰入口" },
    { id = 1, text = "全員到齊，準備進場" },
})
if choice == 0 then
    npc:warp(670010000, 0)
elseif choice == 1 then
    local result = npc:event_start("Amoria")
    local messages = {
        no_party = "請先成立隊伍。",
        not_leader = "請隊長代表與我對話。",
        party_size = "請讓六名隊員都到挑戰入口集合。",
        marriage = "請確認六位隊員皆已婚，而且至少有一位男性和一位女性。",
        level = "隊員必須達到 40 級才能參加。",
        busy = "本頻道已經有隊伍在挑戰。",
    }
    if not result.ok then
        npc:say(messages[result.reason] or "暫時不能進場，請稍後再試。", false, false)
    end
end
