-- v148 Npc.wz/9020006 identifies this interaction as prisonBreak_PQ.
if npc:map_id() ~= 921160700 then return end
local result = npc:escape_finish()
if result == "completed" then
    npc:say("謝謝你們救了我！請收下 #b#t4001534##k。", false, true)
    npc:event_leave()
elseif result == "boss" then
    npc:say("要先打倒教官亞尼，才能讓大家重獲自由。", false, false)
elseif result == "inventory" then
    npc:say("請先空出一格其他欄位。", false, false)
end
