-- The lobby, requirements and belts are recorded in v148 Quest.wz/1215.
local belts = {1132094, 1132095, 1132096, 1132097, 1132098}
local choices = {{id = 0, text = "進入隱藏之塔"}}
for i, belt in ipairs(belts) do
    choices[#choices + 1] = {id = i, text = "用 50 個教官的鑰匙兌換 #t" .. belt .. "#"}
end
local choice = npc:menu("想幫忙救出空中監獄的冒險家嗎？請組成 3 至 6 人的隊伍，所有隊員需達到 120 級。", choices)
if choice == 0 then
    local result = npc:event_start("Prison")
    if result.ok then return end
    local reasons = {
        no_party = "請先組成隊伍。",
        not_leader = "請讓你們的隊長來和我說話。",
        party_size = "需要 3 至 6 名隊員，而且所有人都必須在場。",
        level = "所有隊員都需要達到 120 級。",
        busy = "另一隊已進入隱藏之塔，請稍後再來。",
    }
    npc:say(reasons[result.reason] or "現在無法進入隱藏之塔。", false, false)
    return
end
local belt = belts[choice]
if not belt then return end
if npc:item_count(4001534) < 50 then
    npc:say("請帶 50 個 #t4001534# 來找我。", false, false)
elseif not npc:can_hold(belt, 1) then
    npc:say("請先空出一格裝備欄。", false, false)
elseif npc:exchange({items = {{id = 4001534, quantity = -50}, {id = belt, quantity = 1}}}).ok then
    npc:say("這是給你的 #b#t" .. belt .. "##k。", false, false)
end
