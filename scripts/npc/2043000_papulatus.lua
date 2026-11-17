-- Populatus00, the gate to the Origin of Clocktower.
if not npc:ask_yes_no("要前往鐘塔的原動力室嗎？請由隊長帶領 2 至 6 名、15 級以上的隊員進入。") then return end
local result = npc:event_start("Papulatus")
if result.ok then return end
local why = {
    no_party = "請先組隊。",
    not_leader = "請由隊長申請。",
    party_size = "需要 2 至 6 名隊員到場。目前有 " .. result.here .. " 人。",
    level = "隊員需要達到 15 級。",
    busy = "目前有隊伍正在挑戰，請稍候。",
}
npc:say(why[result.reason] or "目前無法進場。", false, false)
