-- Legacy 2159018 and v148 Quest.wz/1218.
local choice = npc:menu("願意幫我解除冰騎士的詛咒嗎？請組成 3 至 6 人的隊伍，所有成員需達到 50 級。", {
    { id = 0, text = "進入冰騎士的詛咒" },
    { id = 1, text = "用 10 個寒冰兌換 #t1072510#" },
    { id = 2, text = "用 20 個寒冰兌換 #t1032100#" },
})
if choice == 0 then
    local result = npc:event_start("Iceman")
    if result.ok then return end
    local reasons = {
        no_party = "請先組成隊伍。",
        not_leader = "請讓你們的隊長跟我說話。",
        party_size = "需要 3 至 6 名隊員同時在場。",
        level = "所有隊員都需要達到 50 級。",
        busy = "另一隊已經進場，請稍後再來。",
    }
    npc:say(reasons[result.reason] or "現在無法進入冰雪平原。", false, false)
elseif choice == 1 or choice == 2 then
    local item = choice == 1 and 1072510 or 1032100
    local price = choice == 1 and 10 or 20
    if npc:item_count(4001529) < price then
        npc:say("請帶 " .. price .. " 個 #t4001529# 來找我。", false, false)
    elseif not npc:can_hold(item, 1) then
        npc:say("請先空出裝備欄位。", false, false)
    elseif npc:exchange({items = {{id = 4001529, quantity = -price}, {id = item, quantity = 1}}}).ok then
        npc:say("這是給你的 #b#t" .. item .. "##k。", false, false)
    end
end
