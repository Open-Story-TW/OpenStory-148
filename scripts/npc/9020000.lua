-- Source: Legacy scripts/npc/9020000.js (拉克里斯, 第一次同行)
-- Source SHA256: 4fc99cb5daa4e2d4e86fdcf24b4f8029c45d69223e4e7ebde8ede096c31a674b
-- Entrance of the Kerning party quest. The party, level and vacancy checks
-- are the server's (event "KerningPQ"); this script only words the answer.
local EVENT, LOBBY = "KerningPQ", 910340700
local PASS, COUPON, LIQUID, SHOES = 4001008, 4001007, 4001531, 1072533

-- Passes and coupons of an earlier attempt are worthless outside.
local leftovers = {}
for _, id in ipairs({ PASS, COUPON }) do
    local count = npc:item_count(id)
    if count > 0 then
        leftovers[#leftovers + 1] = { id = id, quantity = -count }
    end
end
if #leftovers > 0 then
    npc:exchange({ items = leftovers })
end

if npc:map_id() ~= LOBBY then
    if npc:ask_yes_no("要前往組隊任務的等候地圖嗎？") then
        npc:warp(LOBBY, 0)
    end
    return
end

local choice = npc:menu("和隊員們同心協力完成任務如何？裡面有許多沒有默契就過不了的關卡。想挑戰的話，請讓#b隊長#k來跟我說話。", {
    { id = 0, text = "我要挑戰組隊任務" },
    { id = 1, text = "我想要#t1072533#" },
})
if choice == 1 then
    if not npc:can_hold(SHOES, 1) then
        npc:say("請先空出裝備欄位。", false, false)
    elseif npc:item_count(LIQUID) < 10 then
        npc:say("收集 10 個 #b#t4001531##k 再來找我吧。", false, false)
    elseif npc:exchange({ items = { { id = LIQUID, quantity = -10 }, { id = SHOES, quantity = 1 } } }).ok then
        npc:say("這是給您的 #b#t1072533##k。", false, false)
    else
        npc:say("交換失敗了，請確認背包後再試一次。", false, false)
    end
    return
end

local result = npc:event_start(EVENT)
if result.ok then
    return
end
local answers = {
    unknown = "目前無法進行這個組隊任務。",
    unavailable = "目前無法進場，請稍後再試。",
    no_party = "必須先組成隊伍，再由#b隊長#k來跟我說話。",
    not_leader = "想挑戰的話，請讓#b隊長#k來跟我說話。",
    busy = "已經有其他隊伍正在挑戰了，請稍後再來。",
    party_size = "隊伍必須有 3 到 6 人，而且都要在這張地圖上。目前這裡有 #b" .. result.here .. "#k 位隊員。",
    level = "有隊員的等級不符（需要 20 級以上）。這裡的 #b" .. result.here .. "#k 位隊員中有 #b" .. result.qualified .. "#k 位符合。",
}
npc:say(answers[result.reason] or answers.unknown, false, false)
