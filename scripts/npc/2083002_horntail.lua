-- 根源水晶，離開普通或混沌闇黑龍王洞穴。
if npc:event_status("Horntail").inside or npc:event_status("ChaosHorntail").inside then
    if npc:ask_yes_no("確定要離開闇黑龍王的洞穴嗎？請先拾取戰利品。") then
        npc:warp(240050400, 0)
    end
end
