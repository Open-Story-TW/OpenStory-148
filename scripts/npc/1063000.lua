-- TWMS v148 Map.wz/910530001 and Quest.wz/Check.img item for quest 2052.
if npc:map_id() ~= 910530001 then
    return
end
local missing = 10 - npc:item_count(4031025)
if npc:quest_state(2052) == 1 and missing > 0 then
    if not npc:exchange({items = {{id = 4031025, quantity = missing}}}).ok then
        npc:say("請先空出其他欄位，好把花帶回去。", false, false)
        return
    end
    npc:say("你採齊了約翰要的 #b#t4031025##k。", false, false)
elseif not npc:ask_yes_no("要離開森林，回奇幻村嗎？") then
    return
end
npc:warp(105000000, 0)
