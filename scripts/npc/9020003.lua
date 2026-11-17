
if npc:map_id() ~= 923040000 then
    return
end

local option = npc:menu("陷入危險的坎特需要 3 至 6 名 120 級以上的隊員，限時 20 分鐘。", {
    { id = 0, text = "與隊友前往危險之海" },
    { id = 1, text = "以 100 個 #t4001535# 交換 #t1022123#" },
})
if option == 1 then
    local exchange = npc:exchange({
        items = {
            { id = 4001535, quantity = -100 },
            { id = 1022123, quantity = 1 },
        },
    })
    npc:say(exchange.ok and "你獲得了 #t1022123#。" or "請確認持有 100 個 #t4001535#，並預留裝備欄位。", false, false)
    return
end
if option ~= 0 then
    return
end

local admit = npc:event_start("KentaPQ")
if admit.ok then
    return
end
local reason = {
    no_party = "請先組隊。",
    not_leader = "請讓隊長申請入場。",
    party_size = "需要 3 至 6 名隊員一起申請。",
    level = "所有入場的隊員都需要達到 120 級。",
    busy = "這個頻道已有隊伍在挑戰。",
}
npc:say(reason[admit.reason] or "目前無法進入危險之海。", false, false)
