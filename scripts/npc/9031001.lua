-- Source: Legacy scripts/npc/9031001.js (斯塔切, 藥草採集大師)
-- Source SHA256: 916acd4977ed92ef8b54a7901247e1b5d15486b8c69c131f3a9a6e1988016358
-- Teaches or removes 藥草採集, and trades 100 #t4022023# for one #t2028066#.
-- The profession rules themselves are checked again by the server on commit.
local SKILL, OTHER = 92000000, 92010000
local CRAFTS = { 92020000, 92030000, 92040000 }
local SCRAP, BAG = 4022023, 2028066
local choice = npc:menu("我是藥草採集大師#b斯塔切#k。有什麼事嗎？", {
    { id = 0, text = "學習/放棄 藥草採集" },
    { id = 1, text = "用 100 個 #t4022023# 交換 #t2028066#" },
})
if choice == 0 then
    for _, craft in ipairs(CRAFTS) do
        if npc:profession_level(craft) > 0 then
            npc:say("請先放棄 裝備製作/飾品製作/鍊金術。", false, false)
            return
        end
    end
    if npc:profession_level(SKILL) > 0 then
        if npc:ask_yes_no("您確定要放棄 藥草採集 嗎？您將會失去所有 藥草採集 的熟練度與等級。") then
            if npc:profession_forget(SKILL).ok then
                npc:say("您放棄了 藥草採集。", false, false)
            else
                npc:say("現在無法放棄 藥草採集，請稍後再試。", false, false)
            end
        end
    elseif npc:profession_level(OTHER) > 0 then
        npc:say("您不能學習 藥草採集，因為您已經學會了 採礦。", false, false)
    elseif npc:ask_yes_no("您確定要學習 藥草採集 嗎？") then
        if npc:profession_learn(SKILL).ok then
            npc:say("您學會了 藥草採集。\r\n帶著#b#t1502000##k到藥草田試試看吧！", false, false)
        else
            npc:say("現在無法學習 藥草採集，請稍後再試。", false, false)
        end
    end
else
    if npc:item_count(SCRAP) < 100 then
        npc:say("您需要 100 個 #b#t4022023##k。", false, false)
    elseif not npc:can_hold(BAG, 1) then
        npc:say("請先空出消耗欄位。", false, false)
    elseif npc:exchange({ items = { { id = SCRAP, quantity = -100 }, { id = BAG, quantity = 1 } } }).ok then
        npc:say("謝謝，這是給您的 #b#t2028066##k。", false, false)
    else
        npc:say("交換失敗了，請確認背包後再試一次。", false, false)
    end
end
