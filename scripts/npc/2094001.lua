-- Legacy 2094001 and the three TWMS v148 reward hats from Quest.wz/1204.
if npc:map_id() ~= 925100600 then return end
local choice = npc:menu("謝謝你們幫忙擊敗金勾海賊王。", {
    {id = 0, text = "我要回到入口"},
    {id = 1, text = "我要兌換海賊王的帽子"},
})
if choice == 0 then
    npc:warp(251010404, 0)
elseif choice == 1 then
    local nextHat, previous = 1002573, 0
    if npc:item_count(1003267) > 0 then
        npc:say("你已經有最高階的帽子了。", false, false)
        return
    elseif npc:item_count(1002574) > 0 then
        nextHat, previous = 1003267, 1002574
    elseif npc:item_count(1002573) > 0 then
        nextHat, previous = 1002574, 1002573
    end
    if npc:item_count(4001455) < 20 then
        npc:say("請帶 20 個 #t4001455# 來交換。", false, false)
        return
    end
    if not npc:can_hold(nextHat, 1) then
        npc:say("請先空出裝備欄位。", false, false)
        return
    end
    local items = {{id = 4001455, quantity = -20}, {id = nextHat, quantity = 1}}
    if previous > 0 then items[#items + 1] = {id = previous, quantity = -1} end
    if npc:exchange({items = items}).ok then
        npc:say("這是你的 #b#t" .. nextHat .. "##k。", false, false)
    end
end
