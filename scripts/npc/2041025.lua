-- Machine Apparatus, inside Origin of Clocktower.
if npc:map_id() ~= 220080001 or not npc:event_status("Papulatus").inside then return end
if npc:ask_yes_no("嘟……想要離開原動力室嗎？請記得先拾取戰利品。") then
    npc:event_leave()
end
