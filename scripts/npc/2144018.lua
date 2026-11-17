-- 阿卡伊農入口與出口；此對話亦可由時空裂縫打開。
local status = npc:event_status("Arkarium")
if status.inside then
    if npc:ask_yes_no("確定要離開阿卡伊農遠征嗎？") then npc:event_leave() end
    return
end
if npc:map_id() ~= 272020110 or not npc:ask_yes_no("要前往阿卡伊農的祭壇嗎？請建立 3 至 18 名、120 至 200 級的阿卡伊農遠征隊，再由隊長申請。") then
    return
end
local r = npc:event_start("Arkarium")
if r.ok then return end
local why = {
    no_expedition = "請先建立阿卡伊農遠征隊。",
    expedition_type = "這裡只能由阿卡伊農遠征隊進入。",
    not_leader = "請由遠征隊隊長申請。",
    party_size = "需要 3 至 18 人到場。目前有 " .. r.here .. " 人。",
    roster_absent = "請等全員到齊後再進場。",
    level = "遠征隊員需要 120 至 200 級。",
    busy = "已經有遠征隊正在祭壇內挑戰。",
}
npc:say(why[r.reason] or "目前無法進場。", false, false)
