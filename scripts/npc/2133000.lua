-- Legacy Ellin lobby and TWMS v148 Quest.wz/1206.
if npc:map_id() ~= 300030100 then return end
local choice = npc:menu("願意幫忙淨化森林嗎？請集合 3 至 5 位 70 級以上的隊員。", {
    {id = 0, text = "進入毒霧森林"},
    {id = 1, text = "兌換 #t1032060#"},
    {id = 2, text = "兌換 #t1032061#"},
    {id = 3, text = "兌換 #t1032101#"},
})
if choice == 0 then
    local entry = npc:event_start("Ellin")
    if entry.ok then return end
    local reason = {
        no_party = "請先組隊。",
        not_leader = "請讓隊長跟我說話。",
        party_size = "需要 3 至 5 名隊員同時在場。",
        level = "所有隊員需達到 70 級。",
        busy = "另一隊正在森林裡，請稍後再來。",
    }
    npc:say(reason[entry.reason] or "現在無法進入森林。", false, false)
elseif choice ~= nil then
    local reward = ({1032060, 1032061, 1032101})[choice]
    local previous = ({[2] = 1032060, [3] = 1032061})[choice]
    if npc:item_count(reward) > 0 then
        npc:say("你已經有這件耳環了。", false, false)
        return
    end
    if npc:item_count(4001198) < 10 or (previous and npc:item_count(previous) == 0) then
        npc:say("請帶 10 個 #t4001198# 與需要交換的耳環。", false, false)
        return
    end
    if not npc:can_hold(reward, 1) then
        npc:say("請先空出裝備欄位。", false, false)
        return
    end
    local items = {{id = 4001198, quantity = -10}, {id = reward, quantity = 1}}
    if previous then items[#items + 1] = {id = previous, quantity = -1} end
    npc:exchange({items = items})
end
