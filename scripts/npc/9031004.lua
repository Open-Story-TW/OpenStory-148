-- Source: Legacy scripts/npc/9031004.js (梅爾茨, 飾品製作大師)
-- Source SHA256: 28543071315f04b93c366b5598f13673a09e168574eb08473441d6591b2db5c3
-- Teaches or removes 飾品製作. A character holds one craft, and needs
-- 採礦 first; the server checks the same rules again on commit.
local SKILL, NEED = 92030000, 92010000
local OTHERS = { 92020000, 92040000 }
npc:menu("我是飾品製作大師#b梅爾茨#k。有什麼事嗎？", {
    { id = 0, text = "學習/放棄 飾品製作" },
})
if npc:profession_level(SKILL) > 0 then
    if npc:ask_yes_no("您確定要放棄 飾品製作 嗎？您將會失去所有 飾品製作 的熟練度與等級。") then
        if npc:profession_forget(SKILL).ok then
            npc:say("您放棄了 飾品製作。", false, false)
        else
            npc:say("現在無法放棄 飾品製作，請稍後再試。", false, false)
        end
    end
elseif npc:profession_level(OTHERS[1]) > 0 or npc:profession_level(OTHERS[2]) > 0 or npc:profession_level(NEED) <= 0 then
    npc:say("您不能學習 飾品製作，因為您已經學會了 裝備製作 或 鍊金術，或是您還沒有學習 採礦。", false, false)
elseif npc:ask_yes_no("您確定要學習 飾品製作 嗎？") then
    if npc:profession_learn(SKILL).ok then
        npc:say("您學會了 飾品製作。\r\n到旁邊的#b#p9031013##k就可以開始製作了！", false, false)
    else
        npc:say("現在無法學習 飾品製作，請稍後再試。", false, false)
    end
end
