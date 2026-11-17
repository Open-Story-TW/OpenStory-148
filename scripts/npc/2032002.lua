-- 廢礦調查的受理、交付及離場。
if npc:map_id() ~= 280010000 or not npc:event_status("ZakumMine").inside then return end
local selection = npc:menu("調查進行得怎樣？", {
    {id = 0, text = "查看任務說明"},
    {id = 1, text = "交付火石的母礦"},
    {id = 2, text = "離開廢礦"},
})
if selection == 0 then
    npc:say("找出 7 把 #t4001016#，把它們放在第 16 洞穴的大箱子旁。帶回 #t4001018# 就能完成調查。如果還帶回 30 份 #t4001015#，我會給你們額外獎勵。", false, false)
elseif selection == 1 and npc:ask_yes_no("要把 #t4001018# 交給我嗎？") then
    local result = npc:zakum_mine_finish()
    local reasons = {
        leader = "請由隊長來交付。",
        ore = "請帶回 #t4001018#。",
        bags = "請確認隊員的背包有足夠的空位。",
        unavailable = "目前無法交付，請稍後再試一次。",
    }
    if reasons[result] then npc:say(reasons[result], false, false) end
elseif selection == 2 and npc:ask_yes_no("確定要離開這次調查嗎？") then
    npc:event_leave()
end
