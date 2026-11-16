local ticket = 4031732
local choice = npc:menu("歡迎搭機前往 #b#m103000000##k。你需要什麼服務？", {
    {id = 0, text = "購買機票"},
    {id = 1, text = "進入候機室"}
})
if choice == 0 then
    if npc:ask_yes_no("要花 20,000 楓幣購買一張機票嗎？") then
        local result = npc:exchange({mesos = -20000, items = {{id = ticket, quantity = 1}}})
        if not result.ok then
            npc:say("請確認有足夠的楓幣和其他欄位空間。", false, false)
        end
    end
elseif choice == 1 then
    if npc:item_count(ticket) == 0 then
        npc:say("請先購買機票。", false, false)
    elseif npc:transit_status() ~= "boarding" then
        npc:say("本班機已停止登機，請等下一班。", false, false)
    elseif npc:ask_yes_no("你要使用機票進入候機室嗎？") and not npc:transit_board() then
        npc:say("無法登機，請確認機票或等候下一班。", false, false)
    end
end
