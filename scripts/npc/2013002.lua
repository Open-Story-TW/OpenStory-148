local map = npc:map_id()
if map ~= 920010100 and map ~= 920011300 then
    return
end

local status = npc:orbis_stage()
if status == "reward" then
    npc:say("你完成了女神的痕跡，獲得一個女神的羽翼。", false, false)
    npc:warp(200080101, 0)
elseif status == "not_leader" then
    npc:say("請讓隊長帶大家前往獎勵關卡。", false, false)
end
