-- Source: Legacy scripts/npc/2010008.js
-- Source SHA256: b2f948b09cff90712a660e6c24ca1de94e2ef9641be921a097aa4de25c0e453f
-- Guild emblem. The editor (GUILD_OPERATION 18) submits the emblem command,
-- which charges 1,500,000 mesos in the guild office.
local selected = npc:menu("您想要做什麼?", {{id = 0, text = "新增/修改 公會標誌"}})
if selected == nil then return end
if npc:guild_rank() ~= 1 then
    npc:say("您必須是公會長.  請公會長來找我對話.", false, false)
elseif npc:ask_yes_no("新增/修改 公會標誌需要 #b1,500,000 楓幣#k, 您確定要繼續?") then
    npc:guild_prompt(18)
end
