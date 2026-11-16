-- 梅莉，怪物公園入場券。
local offers = {
    {piece = 4001513, ticket = 4001514, price = 50000},
    {piece = 4001515, ticket = 4001516, price = 100000},
    {piece = 4001521, ticket = 4001522, price = 200000},
}
local action = npc:menu("歡迎來到怪物公園！您需要哪種入場券？", {
    {id = 1, text = "用票券碎片兌換入場券"},
    {id = 2, text = "用楓幣購買入場券"},
})
local choices = {}
for i, offer in ipairs(offers) do
    local cost = action == 1 and "（10 個 #t" .. offer.piece .. "#）" or "（" .. offer.price .. " 楓幣）"
    choices[i] = {id = i, text = "#t" .. offer.ticket .. "#" .. cost}
end
local offer = offers[npc:menu("請選擇票券。每日最多可用楓幣購買 3 張入場券。", choices)]
if not offer then
    return
end
local options = {items = {{id = offer.ticket, quantity = 1}}}
if action == 1 then
    options.items[2] = {id = offer.piece, quantity = -10}
else
    options.mesos = -offer.price
    options.logs = {{scope = "character", period = "day", name = "monster_park_tickets", delta = 1, maximum = 3}}
end
if npc:exchange(options).ok then
    npc:say("已取得 #b#t" .. offer.ticket .. "##k，祝您挑戰順利！", false, false)
else
    npc:say("目前無法取得入場券。請確認票券碎片、楓幣及背包空間，或確認是否已達今日購票上限。", false, false)
end
