-- TWMS v148 Map.wz/910130102 and Quest.wz/Check.img item for quest 2051.
if npc:map_id() ~= 910130102 then
    return
end
if npc:quest_state(2051) == 1 and npc:item_count(4031032) == 0 then
    if not npc:exchange({items = {{id = 4031032, quantity = 1}}}).ok then
        npc:say("請先空出其他欄位，好把草藥帶回去。", false, false)
        return
    end
    npc:say("你找到 #b#t4031032##k，可以帶回去給薩比特拉瑪了。", false, false)
elseif not npc:ask_yes_no("要離開忍耐之林，回魔法森林嗎？") then
    return
end
npc:warp(101000000, 0)
