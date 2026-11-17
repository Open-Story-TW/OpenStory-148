local map = npc:map_id()
if map == 926100000 then
    npc:say("請調查研究室裡的書堆，找出隱藏的開關。", false, false)
    return
end
if map == 926100001 then
    npc:say("請擊退這個房間內所有的怪物。", false, false)
    return
end
if map == 926100600 then
    npc:say("非常感謝你救了我們。真的很感激你的幫忙。", false, false)
    return
end
if map ~= 261000011 then
    return
end

local selection = npc:menu("需要四名 70 級以上的隊員一起進場，限時 20 分鐘。", {
    { id = 0, text = "跟羅密歐調查研究室" },
})
if selection ~= 0 then
    return
end
local result = npc:event_start("RomeoPQ")
if not result.ok then
    npc:say("請由隊長帶齊四名 70 級以上的隊員申請，並確認研究室目前可進入。", false, false)
end
