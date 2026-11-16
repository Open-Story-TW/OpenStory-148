-- Source: Legacy scripts/npc/2040044.js and TWMS v148 Quest.wz/1202.
local state = npc:ludi_stage().state
if state ~= "finished" and state ~= "unavailable" then
    npc:say("擊敗 #b#o9300012##k 就能關閉時空裂縫。請全隊回到這裡，再由隊長來找我。", false, false)
end
