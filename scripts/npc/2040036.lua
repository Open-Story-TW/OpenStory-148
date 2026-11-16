-- Source: Legacy scripts/npc/2040036.js and TWMS v148 Quest.wz/1202.
local state = npc:ludi_stage().state
if state == "cleared" then
    npc:say("發條鼠都擊退了。請利用傳送點前往下一關。", false, false)
elseif state ~= "unavailable" then
    npc:say("請擊退這一層所有的白色和黑色發條鼠，再由隊長與我對話。", false, false)
end
