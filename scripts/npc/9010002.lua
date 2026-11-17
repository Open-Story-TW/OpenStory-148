local status = npc:monster_rush_status()
if status.unavailable then
    npc:say("部分活動地圖暫時無法使用，募款尚未開放。")
    return
end
if status.remaining <= 0 then
    npc:say("本頻道已完成募款。募集結束五分鐘後，怪物會到 #m260000000#、#m680000000#、#m211000000#、#m600000000# 和 #m120000000#。")
    return
end
local amounts = {69, 50000000, 100000000, 200000000, 400000000, 800000000, 1500000000}
local choices = {
    {id = 1, text = "69 楓幣"},
    {id = 2, text = "5,000 萬楓幣"},
    {id = 3, text = "1 億楓幣"},
    {id = 4, text = "2 億楓幣"},
    {id = 5, text = "4 億楓幣"},
    {id = 6, text = "8 億楓幣"},
    {id = 7, text = "15 億楓幣"},
}
local selection = npc:menu("本頻道還需要 " .. status.remaining .. " 楓幣召開怪物來襲。達到目標後，怪物將於五分鐘後出現。", choices)
local amount = amounts[selection]
if not amount then
    return
end
if not npc:ask_yes_no("確定捐出最多 " .. amount .. " 楓幣嗎？超過募款目標的部分會留在你的身上。") then
    return
end
local outcome = npc:monster_rush_donate(amount)
if outcome.unavailable then
    npc:say("部分活動地圖暫時無法使用，請稍後再試。")
elseif outcome.paid > 0 then
    npc:say("已收到 " .. outcome.paid .. " 楓幣。謝謝你！")
elseif outcome.reason == "closed" then
    npc:say("本頻道的募集已經結束。")
elseif outcome.reason == "insufficient" then
    npc:say("你身上的楓幣不足。")
else
    npc:say("募款金額剛有變動，請再問我一次。")
end
