-- v148 Quest.wz 26000: 黛依雅的G-藥水.
if not npc:ask_yes_no("本週的公會支援已到。要領取 #v2002037# #bG藥水#k 嗎？") then
    return
end

local receipt = npc:exchange({ quests = { { id = 26000, state = 1 }, { id = 26000, state = 2 } } })
if receipt.ok then
    npc:say("G藥水已放入消耗欄。下週一可以再領取。", false, false)
else
    npc:say("目前無法領取。請確認消耗欄有空間，且本週尚未領取。", false, false)
end
