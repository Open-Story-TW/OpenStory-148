-- Source: Legacy scripts/npc/2040047.js.
local map = npc:map_id()
local PUPPET = 4001454
if map == 922010000 then
    local items = {}
    for _, id in ipairs({4001022, 4001023, PUPPET}) do
        local count = npc:item_count(id)
        if count > 0 then
            items[#items + 1] = {id = id, quantity = -count}
        end
    end
    if #items == 0 or npc:exchange({items = items}).ok then
        npc:warp(221023300, 0)
    end
    return
end
if not npc:event_status("LudiPQ").inside then
    return
end
if map == 922010800 then
    local choice = npc:menu("需要什麼幫助？", {
        { id = 0, text = "我需要踏板娃娃" },
        { id = 1, text = "我想離開副本" },
    })
    if choice == 0 then
        if npc:item_count(PUPPET) > 0 then
            npc:say("請先使用背包中的踏板娃娃。", false, false)
        elseif npc:exchange({items = {{id = PUPPET, quantity = 1}}}).ok then
            npc:say("把踏板娃娃放在踏板上，就能代替一位隊員。", false, false)
        end
        return
    end
end
if npc:ask_yes_no("現在離開的話，就不能回到這一場了。要離開嗎？") then
    npc:event_leave()
end
