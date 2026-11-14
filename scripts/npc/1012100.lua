-- Source: Legacy scripts/npc/1012100.js
-- Source SHA256: d14addd63e540d4a0f2405a71ebf2d68e6f99348c2c544fbbafd547fb1e61440
-- First advancement only. Later quest/job branches remain pending.
local job = npc:job()
if job ~= 0 then
    if job == 300 and npc:level() >= 30 then
        npc:say("我目前只能協助冒險家完成第一次轉職。", false, false)
    else
        npc:say("You have chosen wisely.", false, false)
    end
    return
end
if npc:level() < 10 then
    npc:say("Train a bit more and I can show you the way of the #rBowman#k.", false, false)
    return
end
while true do
    npc:say("So you decided to become a #rBowman#k?")
    if npc:say("It is an important and final choice. You will not be able to turn back.", true, true) then
        break
    end
end
if not npc:ask_yes_no("Do you want to become a #rBowman#k?") then
    npc:say("Make up your mind and visit me again.", false, false)
    return
end
-- The script's pre-change reset is overwritten by Java changeJob's 4/4/4/4.
local result = npc:advance_first_job(300)
if result.ok then
    npc:say("So be it! Now go, and go with pride.", false, false)
else
    npc:say("請確認背包仍有空間，並且已符合轉職條件。", false, false)
end
