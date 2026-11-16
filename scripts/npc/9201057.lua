local map = npc:map_id()
if map == 600010002 or map == 600010004 then
    if npc:ask_yes_no("要離開候車區嗎？離開後車票無法退還。") and not npc:transit_leave() then
        npc:say("這班車已出發，請等候抵達。", false, false)
    end
    return
end
local ticket, destination
if map == 103020000 then
    ticket, destination = 4031711, 600000000
elseif map == 600010001 then
    ticket, destination = 4031713, 103000000
else
    return
end
if npc:ask_yes_no("要花 5,000 楓幣購買一張開往 #b#m" .. destination .. "##k 的車票嗎？") then
    local result = npc:exchange({mesos = -5000, items = {{id = ticket, quantity = 1}}})
    if not result.ok then
        npc:say("請確認有足夠的楓幣和其他欄位空間。", false, false)
    end
end
