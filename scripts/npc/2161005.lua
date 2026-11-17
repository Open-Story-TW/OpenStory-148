-- 凡雷恩遠征入口與出口。
local status = npc:event_status("VonLeon")
if status.inside then
    if npc:ask_yes_no("確定要離開凡雷恩遠征嗎？") then npc:event_leave() end
    return
end
if npc:map_id() ~= 211070000 or not npc:ask_yes_no("要進入凡雷恩的謁見室嗎？請建立 6 至 18 名、120 至 200 級的凡雷恩遠征隊，再由隊長申請。") then
    return
end
local r = npc:event_start("VonLeon")
if r.ok then return end
local why = {
    no_expedition = "請先建立凡雷恩遠征隊。",
    expedition_type = "這裡只能由凡雷恩遠征隊進入。",
    not_leader = "請由遠征隊隊長申請。",
    party_size = "需要 6 至 18 人到場。目前有 " .. r.here .. " 人。",
    roster_absent = "請等全員到齊後再進場。",
    level = "遠征隊員需要 120 至 200 級。",
    busy = "已經有遠征隊正在房內挑戰。",
}
npc:say(why[r.reason] or "目前無法進場。", false, false)
