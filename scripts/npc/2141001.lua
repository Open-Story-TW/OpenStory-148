-- 皮卡啾遠征入口。
if npc:map_id() ~= 270050000 or not npc:ask_yes_no("要挑戰皮卡啾嗎？請建立 3 至 30 名、120 至 200 級的皮卡啾遠征隊，再由隊長申請。") then
    return
end
local r = npc:event_start("PinkBean")
if r.ok then return end
local why = {
    no_expedition = "請先建立皮卡啾遠征隊。",
    expedition_type = "這裡只能由皮卡啾遠征隊進入。",
    not_leader = "請由遠征隊隊長申請。",
    party_size = "需要 3 至 30 人到場。目前有 " .. r.here .. " 人。",
    roster_absent = "請等全員到齊後再進場。",
    level = "遠征隊員需要 120 至 200 級。",
    busy = "已經有遠征隊正在裡面挑戰。",
}
npc:say(why[r.reason] or "目前無法進場。", false, false)
