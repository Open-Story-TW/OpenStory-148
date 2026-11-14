-- Source: Legacy scripts/npc/9031003.js (愛爾森, 裝備製作大師)
-- Source SHA256: dd1486e25c51e1811f6506de13e2f85075a5a5409e940baaa6d5ad08d468fc87
-- Teaches or removes 裝備製作. A character holds one craft, and needs
-- 採礦 first; the server checks the same rules again on commit.
local SKILL, NEED = 92020000, 92010000
local OTHERS = { 92030000, 92040000 }
npc:menu("我是裝備製作大師#b愛爾森#k。有什麼事嗎？", {
    { id = 0, text = "學習/放棄 裝備製作" },
})
if npc:profession_level(SKILL) > 0 then
    if npc:ask_yes_no("您確定要放棄 裝備製作 嗎？您將會失去所有 裝備製作 的熟練度與等級。") then
        if npc:profession_forget(SKILL).ok then
            npc:say("您放棄了 裝備製作。", false, false)
        else
            npc:say("現在無法放棄 裝備製作，請稍後再試。", false, false)
        end
    end
elseif npc:profession_level(OTHERS[1]) > 0 or npc:profession_level(OTHERS[2]) > 0 or npc:profession_level(NEED) <= 0 then
    npc:say("您不能學習 裝備製作，因為您已經學會了 飾品製作 或 鍊金術，或是您還沒有學習 採礦。", false, false)
elseif npc:ask_yes_no("您確定要學習 裝備製作 嗎？") then
    if npc:profession_learn(SKILL).ok then
        npc:say("您學會了 裝備製作。\r\n到旁邊的#b#p9031012##k就可以開始製作了！", false, false)
    else
        npc:say("現在無法學習 裝備製作，請稍後再試。", false, false)
    end
end
