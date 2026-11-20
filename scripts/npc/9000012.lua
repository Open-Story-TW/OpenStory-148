if npc:map_id() ~= 109080000 then return end

local choice = npc:menu("有什麼能幫你的嗎？", {
    {id = 0, text = "離開打椰子活動。"},
    {id = 1, text = "用 1 楓幣購買木棍。"},
})
if choice == 0 and npc:ask_yes_no("確定要離開活動嗎？") then
    npc:warp(109050001, 0)
elseif choice == 1 then
    local result = npc:exchange({mesos = -1, items = {{id = 1322005, quantity = 1}}})
    if result.ok then
        npc:say("木棍交給你了。", false, false)
    else
        npc:say("請確認楓幣足夠，而且裝備欄有空位。", false, false)
    end
end
