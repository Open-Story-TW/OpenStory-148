-- Source: Legacy scripts/npc/2007.js
-- Source SHA256: 552b9429f8b05a6497dc78769d160b163952053e2741cce629a71ffb0fed77bb
if npc:level() ~= 1 or npc:map_id() ~= 10000 then
    return
end
local destination = npc:menu("你想前往哪裡？", {
    { id = 3, text = "去維多利亞" },
    { id = 4, text = "待在楓之島" },
})
if destination == 3 then
    npc:warp(104000000, 0)
end
