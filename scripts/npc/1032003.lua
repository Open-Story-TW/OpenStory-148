-- 賽恩的入場條件沿用舊腳本。v148 QuestInfo 2050／2051 說明完成後可免費回訪。
local level = npc:level()
if level < 25 then
    npc:say("你的等級必須更高才能進入這片森林。", false, false)
    return
end

local quest, course = 2051, 910130100
if npc:quest_state(2050) == 1 or level < 50 then
    quest, course = 2050, 910130000
end

local prompt = "要支付 #b5000#k 楓幣進入 #b#m" .. course .. "##k 嗎？"
local free = npc:quest_state(quest) == 2
if free then
    prompt = "你已經完成了薩比特拉瑪的委託。要免費進入 #b#m" .. course .. "##k 嗎？"
end
if not npc:ask_yes_no(prompt) then
    return
end
if not free and not npc:exchange({mesos = -5000}).ok then
    npc:say("你的楓幣不夠，沒辦法進入。", false, false)
    return
end
npc:warp(course, 0)
