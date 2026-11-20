-- 林車長，堆積灰塵的月台。
local lobby, failed, cleared = 910320000, 910320001, 910330001
local map = npc:map_id()
if map == cleared then
    local due = npc:log("character", "permanent", "subway_boarding_passes")
    if due > 0 then
        if not npc:ask_yes_no("你清理了月台，現在要領取 #b#t4001321##k 嗎？") then return end
        local reward = npc:exchange({
            items = {{id = 4001321, quantity = 1}},
            logs = {{scope = "character", period = "permanent", name = "subway_boarding_passes",
                     expected = due, delta = -1}},
        })
        if not reward.ok then
            npc:say("請在其他欄留一格空位。", false, false)
            return
        end
        npc:say("你已領取 #b#t4001321##k。", false, true)
    end
    npc:event_leave()
elseif map == failed then
    if npc:ask_yes_no("要返回地鐵站嗎？") then npc:event_leave() end
elseif map == 910320100 or map == 910320200 or map == 910320300 then
    if npc:ask_yes_no("確定要離開堆積灰塵的月台？") then npc:event_leave() end
elseif map == lobby then
    if not npc:ask_yes_no("要挑戰堆積灰塵的月台嗎？等級 25 到 30 的勇士可以單獨挑戰或由隊長帶隊前往。") then return end
    local result = npc:event_start("SubwaySurvival")
    if result.ok then return end
    local messages = {
        not_leader = "請由隊長報名。",
        level = "在場的每位隊員都必須介於 25 到 30 級。",
        busy = "目前月台有人正在挑戰，請稍候。",
    }
    npc:say(messages[result.reason] or "現在無法進入月台。", false, false)
end
