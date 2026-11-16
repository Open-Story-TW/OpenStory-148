local ticket = npc:map_id() == 103020000 and 4031711 or 4031713
local phase = npc:transit_status()
if phase ~= "boarding" then
    npc:say("本班車停止上車，請等下一班。", false, false)
elseif npc:item_count(ticket) == 0 then
    npc:say("請先到售票處買車票。", false, false)
elseif npc:ask_yes_no("要使用車票上車嗎？") and not npc:transit_board() then
    npc:say("無法上車，請確認車票或等候下一班。", false, false)
end
