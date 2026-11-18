-- Retained Monster Carnival II result and departure NPC from v148 WZ.
if not npc:carnival_ii_enabled() then
    npc:say("怪物擂台賽 II 目前沒有開放。", false, false)
    return
end

local map = npc:map_id()
local text = "要回到擂台賽大廳嗎？"
if map % 1000 == 300 or map % 1000 == 400 then
    text = "要結算這場擂台賽的經驗值，並前往出口嗎？"
end
if npc:ask_yes_no(text) then
    npc:carnival_ii_exit()
end
