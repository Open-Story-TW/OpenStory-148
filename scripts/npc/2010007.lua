-- Source: Legacy scripts/npc/2010007.js
-- Source SHA256: 4e07325fadd0f0cd52329b1034d6bd4137d40eec0cee565475863d2281df8237
-- Guild office. Creation asks the client for a name (GUILD_OPERATION 1);
-- the fee is charged by the durable create command (GuildHandler: 5,000,000).
-- The expansion ceilings follow the server's guild_max_capacity setting.
local selected = npc:menu("您想要做什麼?", {
    {id = 0, text = "創建公會"},
    {id = 1, text = "解散公會"},
    {id = 2, text = "使用#r楓幣#b增加公會成員數量限制 (最高提升至" .. npc:guild_capacity_limit(0) .. "人)"},
    {id = 3, text = "使用#rGP#b增加公會成員數量限制 (最高提升至" .. npc:guild_capacity_limit(1) .. "人)"},
})
if selected == nil then return end
local rank = npc:guild_rank()
if selected == 0 then
    if rank > 0 then
        npc:say("您已經加入公會 無法創建新的公會.", false, false)
    elseif npc:ask_yes_no("創建公會需要花費 #b5,000,000 楓幣#k, 您確定要繼續嗎?") then
        npc:guild_prompt(1)
    end
    return
end
if rank ~= 1 then
    if selected == 1 then
        npc:say("只有公會長可以解散公會.", false, false)
    else
        npc:say("只有公會長可以擴增公會成員數量限制.", false, false)
    end
    return
end
if selected == 1 then
    if npc:ask_yes_no("您確定您要解散公會? 此執行無法反悔且您將失去所有的 GP.") then
        npc:guild_disband()
    end
elseif selected == 2 then
    if npc:ask_yes_no("擴增公會成員數量限制 #b5#k 人 - 需要 #b500,000 楓幣#k, 您確定要繼續?") then
        npc:guild_capacity(0)
    end
elseif npc:ask_yes_no("擴增公會成員數量限制 #b5#k 人 - 需要 #b25,000 GP#k, 您確定要繼續?") then
    npc:guild_capacity(1)
end
