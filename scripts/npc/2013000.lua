local map = npc:map_id()

if map == 920010000 then
    npc:say("敲開雲朵，收集 20 個雲朵碎片，然後把碎片放到封印前救出侍從。", false, false)
    return
end

if map ~= 200080101 then
    return
end

local wing = 4001158
local rewards = {
    [1] = { item = 1082232, cost = 15 },
    [2] = { item = 1072455, cost = 10 },
    [3] = { item = 1082322, cost = 30, base = 1082232 },
    [4] = { item = 1072534, cost = 20, base = 1072455 },
}
local option = npc:menu("女神的痕跡需要 3 至 6 名 70 級以上的隊員，限時 20 分鐘。", {
    { id = 0, text = "進入女神之塔" },
    { id = 1, text = "#t1082232#（15 個女神的羽翼）" },
    { id = 2, text = "#t1072455#（10 個女神的羽翼）" },
    { id = 3, text = "以 #t1082232# 與 30 個女神的羽翼交換 #t1082322#" },
    { id = 4, text = "以 #t1072455# 與 20 個女神的羽翼交換 #t1072534#" },
})
if option == nil then
    return
end
local reward = rewards[option]
if reward then
    local items = {
        { id = wing, quantity = -reward.cost },
        { id = reward.item, quantity = 1 },
    }
    if reward.base then
        items[#items + 1] = { id = reward.base, quantity = -1 }
    end
    if npc:exchange({ items = items }).ok then
        npc:say("你獲得了 #t" .. reward.item .. "#。", false, false)
    else
        npc:say("請確認持有需要的道具，並留出裝備欄位。", false, false)
    end
    return
end
if option ~= 0 then
    return
end

local result = npc:event_start("OrbisPQ")
if not result.ok then
    local messages = {
        no_party = "請先組隊。",
        not_leader = "請讓隊長申請入場。",
        party_size = "需要 3 至 6 名隊員在場。",
        level = "在場隊員都需要達到 70 級。",
        busy = "這個頻道已有隊伍在挑戰。",
    }
    npc:say(messages[result.reason] or "目前無法進入女神之塔。", false, false)
end
