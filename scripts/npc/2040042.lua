-- Source: Legacy scripts/npc/2040042.js and TWMS v148 Quest.wz/1202.
local state = npc:ludi_stage().state
if state == "cleared" then
    npc:say("泥人領導者已經被擊退了。傳送點開啟了！", false, false)
elseif state ~= "unavailable" then
    npc:say("消滅所有的 #b#o9300010##k，再由隊長來找我。", false, false)
end
