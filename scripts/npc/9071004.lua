-- 怪物公園入口。路線的等級、門票與隊伍條件由伺服器驗證。
local routes = {
    {952000000, 20, 30}, {952010000, 45, 55}, {952020000, 50, 60}, {952030000, 55, 65}, {952040000, 60, 70},
    {953000000, 70, 80}, {953010000, 75, 85}, {953020000, 85, 95}, {953030000, 95, 105}, {953040000, 100, 110}, {953050000, 110, 120},
    {954000000, 120, 130}, {954010000, 125, 135}, {954020000, 130, 140}, {954030000, 140, 150}, {954040000, 150, 165}, {954050000, 165, 200},
}
local menu = {}
for i, route in ipairs(routes) do
    menu[i] = {id = i, text = "#m" .. route[1] .. "#（" .. route[2] .. "～" .. route[3] .. " 級）"}
end
local route = routes[npc:menu("請由隊長選擇要挑戰的路線。每位隊員都需要一張對應的入場券。", menu)]
if not route then
    return
end
local result = npc:event_start("MonsterPark_" .. route[1])
if result.ok then
    return
end
local messages = {
    no_party = "請先組成隊伍。",
    not_leader = "請讓隊長選擇挑戰路線。",
    party_size = "請讓 2 到 6 名隊員都到同一張地圖。",
    level = "有隊員的等級不符合這條路線的要求。",
    ticket = "有隊員缺少這條路線的入場券。",
    busy = "有其他隊伍正在挑戰這條路線，請稍後再試。",
}
npc:say(messages[result.reason] or "目前無法進入怪物公園，請稍後再試。", false, false)
