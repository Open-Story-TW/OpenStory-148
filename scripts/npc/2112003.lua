local map = npc:map_id()
if map == 926110000 then
    npc:say("請調查研究室裡的書堆，找出隱藏的開關。", false, false)
    return
end
if map == 926110001 then
    npc:say("請擊退這個房間內所有的怪物。", false, false)
    return
end
if map ~= 261000021 then
    return
end

local selection = npc:menu("需要四名 70 級以上的隊員一起進場，限時 20 分鐘。", {
    { id = 0, text = "跟茱麗葉調查研究室" },
    { id = 1, text = "用 20 顆卡帕萊特之珠交換茱麗葉墜飾" },
    { id = 2, text = "合成永恆之愛的證明" },
})
if selection == 1 then
    local result = npc:exchange({items = {
        {id = 4001160, quantity = -20}, {id = 1122117, quantity = 1},
    }})
    if result.ok then
        npc:say("已兌換茱麗葉墜飾。", false, false)
    else
        npc:say("需要 20 顆卡帕萊特之珠和一格裝備欄位。", false, false)
    end
    return
end
if selection == 2 then
    local result = npc:exchange({items = {
        {id = 1122116, quantity = -1}, {id = 1122117, quantity = -1},
        {id = 1122118, quantity = 1},
    }})
    if result.ok then
        npc:say("已合成永恆之愛的證明。", false, false)
    else
        npc:say("需要羅密歐墜飾與茱麗葉墜飾。", false, false)
    end
    return
end
if selection ~= 0 then
    return
end
local result = npc:event_start("JulietPQ")
if not result.ok then
    npc:say("請由隊長帶齊四名 70 級以上的隊員申請，並確認研究室目前可進入。", false, false)
end
