-- TWMS Legacy exchange and Quest.wz/1209.
if npc:map_id() ~= 211000002 then return end
local choice = npc:menu("你們是來阻止侏儒帝王復活的嗎？", {
    {id = 0, text = "與隊員進入冰谷"},
    {id = 1, text = "領取裝冰河水的空瓶"},
    {id = 2, text = "兌換挑戰獎勵"},
})
if choice == 0 then
    local entry = npc:event_start("Rex")
    if not entry.ok then
        npc:say("請集合 3 至 6 名 120 級以上的隊員，再由隊長進入。", false, false)
    end
elseif choice == 1 then
    if npc:item_count(4032649) == 0 and npc:item_count(2022698) == 0 and npc:can_hold(4032649, 1) then
        npc:exchange({items = {{id = 4032649, quantity = 1}}})
    end
elseif choice == 2 then
    local prize = npc:menu("想兌換什麼獎勵？", {
        {id = 0, text = "#t1032102#"},
        {id = 1, text = "#t1032103#"},
        {id = 2, text = "#t1032104#"},
        {id = 3, text = "#t1902048#"},
    })
    if prize == nil then return end
    local reward = ({1032102, 1032103, 1032104, 1902048})[prize + 1]
    local prior = ({1032077, 1032078, 1032079})[prize + 1]
    local price = prize == 3 and 300 or 50
    if npc:item_count(4001530) < price or (prior and npc:item_count(prior) < 1) then
        npc:say("需要帶齊交換物品。", false, false)
        return
    end
    if not npc:can_hold(reward, 1) then
        npc:say("請先空出欄位。", false, false)
        return
    end
    local items = {{id = 4001530, quantity = -price}, {id = reward, quantity = 1}}
    if prior then items[#items + 1] = {id = prior, quantity = -1} end
    npc:exchange({items = items})
end
