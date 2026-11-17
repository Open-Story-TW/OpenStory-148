-- Legacy 2094000 and TWMS v148 Quest.wz/1204.
if npc:map_id() ~= 251010404 then return end
if not npc:ask_yes_no("要挑戰金勾海賊王嗎？請組成 3 至 6 人的隊伍，每位隊員都需達到 70 級。") then return end
local result = npc:event_start("Pirate")
if result.ok then return end
local reasons = {
    no_party = "請先組隊，再由隊長來找我。",
    not_leader = "請讓你們的隊長來找我。",
    party_size = "請集合 3 至 6 位隊員。",
    level = "每位隊員都需要達到 70 級。",
    busy = "這個頻道已有隊伍在挑戰。",
}
npc:say(reasons[result.reason] or "現在無法進入海賊船。", false, false)
