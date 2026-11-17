-- TWMS v148 q3759e (飛向天空2), with dialogue adapted from Legacy quest 3759.
if npc:quest_state(3759) ~= 1 or npc:item_count(4032531) < 1 then
    npc:say("請先到神木村找塔塔曼村長，帶回 #b#t4032531##k。", false, false)
    return
end
if not npc:ask_yes_no("你帶回 #b#t4032531##k 了。準備好學習飛翔了嗎？") then
    return
end
local result = npc:exchange({
    items = { { id = 4032531, quantity = -1 } },
    quests = { { id = 3759, state = 2 } },
    growth = { skills = { { id = 1026, level = 1, master = 1 } } },
})
if result.ok then
    npc:say("你已經學會 #b飛翔#k。在天空之門使用時會消耗 MP。", false, false)
else
    npc:say("請確認已經完成前一個任務，並帶著 #b#t4032531##k 再來。", false, false)
end
