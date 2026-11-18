-- v148 Quest.wz/QuestInfo.img:1301 and the native Monster Carnival results NPC.
local map = npc:map_id()
if map == 980090000 or map == 980090001 then
    if npc:ask_yes_no("要離開擂台賽等候室嗎？") then
        npc:carnival_leave()
    end
    return
end

if map >= 980000101 and map <= 980002004 then
    if npc:ask_yes_no("這場擂台賽已經結束。要前往出口嗎？") then
        npc:carnival_leave()
    end
    return
end

if map ~= 980000010 then
    npc:say("符合等級的玩家可以在每小時 15 分、45 分透過邀請函參加怪物擂台賽。", false, false)
    return
end

local choice = npc:menu("你要兌換獎品，還是回到原來的地方？", {
    { id = 0, text = "返回原來的地方" },
    { id = 1, text = "#i1122162# #t1122162#（100 枚 #t4001254#）" },
    { id = 2, text = "#i1102327# #t1102327#（150 枚 #t4001254#）" },
})
if choice == 0 then
    npc:carnival_leave()
    return
end

local item, price
if choice == 1 then
    item, price = 1122162, 100
elseif choice == 2 then
    item, price = 1102327, 150
else
    return
end
if npc:item_count(4001254) < price then
    npc:say("請帶足 #t4001254# 再來兌換。", false, false)
elseif not npc:can_hold(item, 1) then
    npc:say("請先空出裝備欄位。", false, false)
elseif npc:exchange({ items = { { id = 4001254, quantity = -price }, { id = item, quantity = 1 } } }).ok then
    npc:say("這是你的 #t" .. item .. "#。", false, false)
end
