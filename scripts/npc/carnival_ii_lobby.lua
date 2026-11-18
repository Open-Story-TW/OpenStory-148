-- Opt-in compatibility using the retained v148 lobby and Piggy2 cpq2 rules.
local choice = npc:menu("第2回怪物擂臺賽（舊版相容）\r\n請由隊長選擇場地。兩支隊伍的人數須相同。", {
    { id = 1, text = "場地 1（每隊 2～4 人）" },
    { id = 2, text = "場地 2（每隊 2～4 人）" },
    { id = 3, text = "場地 3（每隊 3～6 人）" },
    { id = 4, text = "#i1122058# 用 150 枚 #t4001254# 兌換 #t1122058#" },
    { id = 0, text = "回到原來的地方" },
})

if choice == 0 then
    npc:carnival_ii_exit()
elseif choice == 4 then
    if npc:item_count(4001254) < 150 then
        npc:say("請帶足 #t4001254# 再來兌換。", false, false)
    elseif not npc:can_hold(1122058, 1) then
        npc:say("請先空出裝備欄位。", false, false)
    elseif npc:exchange({ items = { { id = 4001254, quantity = -150 }, { id = 1122058, quantity = 1 } } }).ok then
        npc:say("這是你的 #t1122058#。", false, false)
    end
elseif choice and choice >= 1 and choice <= 3 then
    local status = npc:carnival_ii_register(choice)
    local text = {
        party = "請先組隊。",
        leader = "請隊長來與我對話。",
        size = "你們的隊伍人數不符合這個場地的規定。",
        here = "請確認所有隊員都在這個大廳，且符合等級限制。",
        equal = "兩隊必須有相同人數。",
        busy = "這個場地目前有人在使用。",
        challenged = "已向場內的隊伍提出挑戰。請等候對方隊長確認。",
        unavailable = "暫時無法進入這個場地。",
    }
    if text[status] then
        npc:say(text[status], false, false)
    end
end
