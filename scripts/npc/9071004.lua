-- 怪物公園入口。路線的等級、門票與隊伍條件由伺服器驗證。
local tiers = {
    {ticket = 4001514, levels = "30～95 級", routes = {952000000, 952010000, 952040000, 952020000, 952030000, 953000000, 953010000}},
    {ticket = 4001516, levels = "135～165 級", routes = {953020000, 953030000, 953040000, 953050000, 954000000}},
    {ticket = 4001522, levels = "160～200 級", routes = {954010000, 954020000, 954030000, 954040000, 954050000}},
}
local menu = {}
for i, tier in ipairs(tiers) do
    menu[i] = {id = i, text = "#t" .. tier.ticket .. "#（" .. tier.levels .. "）"}
end
local tier = tiers[npc:menu("請選擇挑戰難度。組隊時由隊長選擇，每位參加者都需要一張對應的入場券。", menu)]
if not tier then
    return
end
local routes = {}
for i, map in ipairs(tier.routes) do
    routes[i] = {id = i, text = "#m" .. map .. "#"}
end
local route = tier.routes[npc:menu("請選擇要挑戰的路線。", routes)]
if not route then
    return
end
local result = npc:event_start("MonsterPark_" .. route)
if result.ok then
    return
end
local messages = {
    not_leader = "請讓隊長選擇挑戰路線。",
    party_size = "最多可有 6 名隊員，請全隊先到怪物公園。",
    level = "有隊員的等級不符合這條路線的要求。",
    ticket = "有隊員缺少這條路線的入場券。",
    busy = "有其他隊伍正在挑戰這條路線，請稍後再試。",
}
npc:say(messages[result.reason] or "目前無法進入怪物公園，請稍後再試。", false, false)
