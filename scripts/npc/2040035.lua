-- Source: Legacy scripts/npc/2040035.js (時空的裂縫的通關出口).
if npc:map_id() ~= 922011100 then
    return
end
local items = {}
for _, id in ipairs({4001022, 4001023, 4001454}) do
    local count = npc:item_count(id)
    if count > 0 then
        items[#items + 1] = {id = id, quantity = -count}
    end
end
if #items == 0 or npc:exchange({items = items}).ok then
    npc:warp(221023300, 0)
end
