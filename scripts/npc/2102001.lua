if npc:ask_yes_no("你確定要離開候船室嗎？") and not npc:transit_leave() then
    npc:say("這班已出發，請等候抵達。", false, false)
end
