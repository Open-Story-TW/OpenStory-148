if npc:map_id() == 540010001 and npc:ask_yes_no("要離開候機室嗎？離開後機票無法退還。") and not npc:transit_leave() then
    npc:say("這班飛機已起飛，請等候降落。", false, false)
end
