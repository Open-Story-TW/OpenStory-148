-- Source: Legacy scripts/npc/1063002.js and TWMS v148 Quest.wz/Check.img 2054.
-- The quest in this client requests 30 white flowers.
local missing = 30 - npc:item_count(4031028)
local item, count = 4031028, missing
if npc:quest_state(2054) ~= 1 or missing <= 0 then
    local ores = {4020007, 4020008, 4010006}
    item, count = ores[math.random(#ores)], 2
end
if npc:exchange({items = {{id = item, quantity = count}}}).ok then
    npc:warp(105000000, 0)
else
    npc:say("請先空出其他欄位，好把採到的東西帶走。", false, false)
end
