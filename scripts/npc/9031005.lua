-- Source: Legacy scripts/npc/9031005.js (卡力安, 鍊金術大師)
-- Source SHA256: e93cc3b4c4ebddf2e1abb00672c76f20d2aa95abd7cf248b9a121590716c29d7
-- Teaches or removes 鍊金術. A character holds one craft, and needs
-- 藥草採集 first; the server checks the same rules again on commit.
local SKILL, NEED = 92040000, 92000000
local OTHERS = { 92020000, 92030000 }
npc:menu("我是鍊金術大師#b卡力安#k。有什麼事嗎？", {
    { id = 0, text = "學習/放棄 鍊金術" },
})
if npc:profession_level(SKILL) > 0 then
    if npc:ask_yes_no("您確定要放棄 鍊金術 嗎？您將會失去所有 鍊金術 的熟練度與等級。") then
        if npc:profession_forget(SKILL).ok then
            npc:say("您放棄了 鍊金術。", false, false)
        else
            npc:say("現在無法放棄 鍊金術，請稍後再試。", false, false)
        end
    end
elseif npc:profession_level(OTHERS[1]) > 0 or npc:profession_level(OTHERS[2]) > 0 or npc:profession_level(NEED) <= 0 then
    npc:say("您不能學習 鍊金術，因為您已經學會了 裝備製作 或 飾品製作，或是您還沒有學習 藥草採集。", false, false)
elseif npc:ask_yes_no("您確定要學習 鍊金術 嗎？") then
    if npc:profession_learn(SKILL).ok then
        npc:say("您學會了 鍊金術。\r\n到旁邊的#b#p9031014##k就可以開始製作了！", false, false)
    else
        npc:say("現在無法學習 鍊金術，請稍後再試。", false, false)
    end
end
