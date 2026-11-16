-- Source: Legacy scripts/npc/2040039.js and TWMS v148 Quest.wz/1202.
local state = npc:ludi_stage().state
if state == "cleared" then
    npc:say("門內的怪物都擊退了，可以前往下一關。", false, false)
elseif state ~= "unavailable" then
    npc:say("黑暗房間裡藏著怪物。請清空五扇門後的所有房間，再由隊長來找我。", false, false)
end
