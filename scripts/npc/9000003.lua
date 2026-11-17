if npc:map_id() ~= 109010000 then return end
if npc:item_count(4031019) > 0 then
    npc:say("你已經領取惡魔文件。", false, false)
elseif npc:item_count(4031018) == 0 then
    npc:say("到東郊或南郊打開寶箱，找到藏寶圖後再回來。", false, false)
elseif npc:ask_yes_no("要用藏寶圖換取惡魔文件嗎？") then
    if npc:exchange({ items = {{id = 4031018, quantity = -1}, {id = 4031019, quantity = 1}} }).ok then
        npc:say("你找到了寶藏！這份惡魔文件交給你。", false, false)
    else
        npc:say("請確認藏寶圖仍在背包，而且背包有空位。", false, false)
    end
end
