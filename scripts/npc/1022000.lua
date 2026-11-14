-- Source: Legacy scripts/npc/1022000.js
-- Source SHA256: 65b65e0fd9dc99db938f9c51f3357bfe26701e16b2dc1cfb88550a954d94ca63
-- First warrior advancement only. Later custom-quest stages are not yet migrated.
local job = npc:job()
if job ~= 0 then
    if job == 100 and npc:level() >= 30 then
        npc:say("我目前只能協助冒險家完成第一次轉職。", false, false)
    else
        npc:say("You have chosen wisely.", false, false)
    end
    return
end

if npc:level() < 10 then
    npc:say("Train a bit more and I can show you the way of the #rWarrior#k.", false, false)
    return
end

while true do
    npc:say("So you decided to become a #rWarrior#k?")
    if npc:say("It is an important and final choice. You will not be able to turn back.", true, true) then
        break
    end
end

if not npc:ask_yes_no("Do you want to become a #rWarrior#k?") then
    npc:say("Make up your mind and visit me again.", false, false)
    return
end

-- Stats, SP, HP bonus, inventory expansion and weapon are one durable command.
local result = npc:advance_warrior()
if result.ok then
    npc:say("So be it! Now go, and go with pride.", false, false)
else
    npc:say("請確認裝備欄仍有空間，並且已符合轉職條件。", false, false)
end
