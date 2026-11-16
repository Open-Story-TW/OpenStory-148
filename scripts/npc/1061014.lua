-- 武英：簡易巴洛古遠征隊入口。
if npc:map_id() ~= 105100100 then
    return
end
if not npc:ask_yes_no("要前往巴洛古的祭壇嗎？請組成 3 至 6 人的簡易巴洛古遠征隊，全員到齊後由隊長申請。") then
    return
end
local result = npc:event_start("BalrogEasy")
if result.ok then
    return
end
local answers = {
    no_expedition = "請先建立簡易巴洛古遠征隊，或接受隊長的邀請。",
    expedition_type = "你所在的遠征隊不是簡易巴洛古遠征隊。",
    not_leader = "請由遠征隊隊長申請進場。",
    party_size = "需要 3 至 6 人。目前有 " .. result.here .. " 人到場。",
    roster_absent = "請等全員到齊且準備就緒再出發。",
    level = "全員等級須介於 45 至 200 級。",
    busy = "已有遠征隊在祭壇內戰鬥。",
}
npc:say(answers[result.reason] or "目前無法進場，請稍後再試。", false, false)
