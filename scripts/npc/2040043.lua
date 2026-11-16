-- Source: Legacy scripts/npc/2040043.js and TWMS v148 Quest.wz/1202.
local result = npc:ludi_stage()
if result.state == "cleared" then
    npc:say("答對了。請走上方的傳送點。", false, false)
elseif result.state ~= "unavailable" then
    npc:say("請讓隊員站在答案所對應的數字踏板上，再由隊長來確認。也可以在踏板上放置 #t4001454#。\r\n\r\n#b" .. result.question .. "#k", false, false)
end
