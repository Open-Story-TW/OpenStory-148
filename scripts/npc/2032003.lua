-- 火山的氣息終點獎勵。
if npc:map_id() ~= 280020001 then return end

local cleared, breath = 1800100202, 4031062
npc:say("恭喜您終於到達終點!  獎勵給你吧!", false, true)
npc:say("加油.", true, true)
if npc:item_count(breath) == 0 then
    local reward = {items = {{id = breath, quantity = 1}}}
    local previous = npc:quest_data(cleared)
    if previous == "" then
        reward.exp = 10000
        reward.quest_data = {{id = cleared, data = "2", expected = previous}}
    end
    if not npc:exchange(reward).ok then
        npc:say("您的背包已滿.", false, false)
        return
    end
end
npc:warp(211042300, 0)
