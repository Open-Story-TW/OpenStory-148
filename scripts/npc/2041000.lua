local phase = npc:transit_status()
if phase == "boarding" then
    if npc:ask_yes_no("現在可以搭車。你要前往 #b#m200000121##k 嗎？") and not npc:transit_board() then
        npc:say("搭車時間已過，請等下一班。", false, false)
    end
elseif phase == "departing" then
    npc:say("即將出發，入口已關閉。請等下一班。", false, false)
else
    npc:say("目前正在途中，抵達後會重新開放入口。", false, false)
end
