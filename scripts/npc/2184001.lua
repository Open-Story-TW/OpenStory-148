-- 希拉遠征隊場內的離場 NPC。
if npc:event_status("Hilla").inside and npc:ask_yes_no("確定要離開希拉遠征嗎？離場後將無法回到這次遠征。") then
    npc:event_leave()
end
