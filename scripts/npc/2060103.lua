-- The original TWMS registrar starts a race between two three-person teams.
local map = npc:map_id()
local status
if map == 923020000 then
    local room = npc:menu("請由隊長選擇鬼盜船競賽場地，也可以用披風碎片兌換裝備。", {
        {id = 1, text = "第一場地"}, {id = 2, text = "第二場地"},
        {id = 3, text = "第三場地"}, {id = 4, text = "第四場地"},
        {id = 5, text = "第五場地"},
        {id = 6, text = "以 #i1102227# 和 100 片披風碎片兌換 #t1102302#"},
        {id = 7, text = "以 100 片披風碎片兌換 #t1082400#"},
    })
    if room == 6 or room == 7 then
        local reward = room == 6 and 1102302 or 1082400
        if not npc:ask_yes_no("確定要兌換 #t" .. reward .. "# 嗎？") then return end
        local items = {{id = 4001533, quantity = -100}}
        if room == 6 then items[#items + 1] = {id = 1102227, quantity = -1} end
        items[#items + 1] = {id = reward, quantity = 1}
        if npc:exchange({items = items}).ok then
            npc:say("已經兌換 #t" .. reward .. "#。", false, false)
        else
            npc:say("請確認兌換物品的數量及裝備欄空間。", false, false)
        end
        return
    end
    status = npc:ghost_ship_queue(room)
else
    local waiting = npc:ghost_ship_waiting()
    if waiting == "challenge" then
        local choice = npc:menu("另一支三人隊伍提出挑戰。", {
            {id = 1, text = "接受挑戰"}, {id = 2, text = "拒絕挑戰"},
        })
        status = npc:ghost_ship_accept(choice)
    elseif waiting == "waiting" then
        if npc:ask_yes_no("要離開鬼盜船的等待區嗎？") then npc:event_leave() end
        return
    else
        if npc:ask_yes_no("要離開鬼盜船嗎？") then npc:event_leave() end
        return
    end
end
local messages = {
    party_size = "請讓三名隊員在大廳集合。",
    level = "每名隊員都需要在 120 至 159 級之間。",
    not_leader = "請讓隊長來登記。",
    challenged = "已提出挑戰，請在大廳等候對方隊長答覆。",
    declined = "已拒絕這次挑戰，可以再等下一隊。",
    waiting = "目前沒有其他隊伍提出挑戰。",
    busy = "這個場地目前有人使用，請選擇其他場地。",
    unavailable = "目前無法進入這個場地。",
}
if messages[status] then npc:say(messages[status], false, false) end
