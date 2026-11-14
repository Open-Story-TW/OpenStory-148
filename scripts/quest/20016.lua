-- Reviewed conversion of Legacy scripts/quest/20016.js (對黑暗巫師瞭解嗎？).
-- Original SHA256: 554658e64da93231d173d176c434127e21b1b1579db68cfaa51916bdebe1001f.
-- Legacy stepped back one line on a decline and looped until ESC, and had no
-- branch for status 8, so the talk stopped after the first closing line;
-- here a decline ends the talk and every closing line is shown.
if npc:quest_state(20016) ~= 0 or npc:quest_state(20015) ~= 2 then return end
local lines = {
    "嗨，#h0#。歡迎來到 #p1101000# 騎士團。我的名字是 #p1101002#，目前擔任年幼女皇的策士。哈哈！",
    "我敢肯定你有很多問題，因為一切都發生得太快了。我會一個一個解釋，從你在哪裡、到你在這裡要做什麼。",
    "這座島叫做耶雷弗。",
    "年幼的女皇是楓之谷世界的統治者。什麼？你是第一次聽說她？嗯，她雖然是楓之谷世界的統治者，但並不喜歡掌控一切。她從遠處守望，確保一切安好。至少平常是這樣。",
    "但是現在情況不同了。我們發現楓之谷世界各地都出現了黑魔法師復活的徵兆。我們不能讓黑魔法師回來，再像過去一樣威脅楓之谷！",
    "但那已經是很久以前的事了，現在的人們不明白黑魔法師有多可怕。大家都被如今的和平寵壞了，忘了楓之谷世界曾經有多混亂可怕。如果我們什麼都不做，黑魔法師將再次統治楓之谷世界！",
}
for i, line in ipairs(lines) do
    npc:say(line, i > 1, true)
end
if not npc:ask_accept("以上是我的說明，有問題嗎？ \r\n\r\n#fUI/UIWindow.img/QuestIcon/4/0# \r\n#fUI/UIWindow.img/QuestIcon/8/0# 380 經驗值") then
    npc:say("喔，還有問題嗎？再跟我說話，我會從頭解釋一次。", false, false)
    return
end
npc:exchange({ exp = 380, quests = { { id = 20016, state = 2 } } })
npc:say("我很高興你清楚我們目前的情況，但以你現在的能力，連黑魔法師的爪牙都打不過，更別說黑魔法師本人了。以你現在的等級，要怎麼保護楓之谷世界呢？")
npc:say("雖然你已經被授予爵位，但還不能算是騎士。你連見習騎士都還不是。如果一直維持現在的水準，你就只是 #p1101000# 皇家騎士團的掃地工而已。", true, true)
npc:say("不過沒有人第一天就是強大的騎士。女皇要的不是天生強大的人，而是有勇氣、能透過嚴格訓練成長為強大騎士的人。所以你應該先成為見習騎士。等你達到那個程度，我們再來談你的任務。", true, true)
npc:say("從左側的傳送點前往修煉森林。在那裡你會找到修煉教官 #p1102000#，他會教你如何變強。在你到達10級之前，可別漫無目的地四處閒晃，聽到了嗎？", true, false)
