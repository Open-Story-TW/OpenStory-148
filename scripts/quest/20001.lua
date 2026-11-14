-- Reviewed conversion of Legacy scripts/quest/20001.js (策士那因哈特).
-- Original SHA256: c4aa71fbdeaeae403e97f7982bd04844cae6f9cdc9cd708bda4348a37d2826a5.
-- Legacy stepped back one line when the briefing was declined, so a decline
-- looped until the player pressed ESC; here a decline ends the talk and the
-- quest can be asked again. Its reward branch ran only for an already
-- finished quest (isQuestCompleted before forceCompleteQuest), so it never
-- paid and is left out. The English Legacy lines are given in Chinese.
-- Check.img: 20000 finished and none of the newer Kimu tutorial quests
-- (20010-20020) started.
if npc:quest_state(20001) ~= 0 or npc:quest_state(20000) ~= 2 then return end
for id = 20010, 20020 do
    if npc:quest_state(id) ~= 0 then return end
end
local lines = {
    "你好，#h #。我正式歡迎你加入皇家騎士團。我是年幼女皇的首席策士，那因哈特。以後會常常見面，記住我的名字吧。哈哈…",
    "我知道你還沒有足夠的時間了解身為皇家騎士該做什麼。我會一件一件仔細說明：這裡是哪裡、年幼女皇是誰，以及我們的職責。",
    "你現在站的地方是耶雷弗，是唯一由年幼女皇直接治理、漂浮在空中的島嶼。沒錯，我們正浮在空中。為了女皇，這座島平常就像一艘船一樣，在楓之谷世界各地航行。",
    "年幼女皇正是楓之谷世界的統治者。什麼？你從沒聽說過？這也難怪。女皇雖然治理這個世界，但並不是高高在上的獨裁者。她以耶雷弗為據點，以旁觀者的身分守望著世界。",
    "但有時候，女皇也不得不親自出手。邪惡的黑魔法師正在世界各地出現復活的徵兆。那個曾經差點毀滅世界的破壞之王，正試圖重新回到我們的生活中。",
    "問題是，沒有人察覺到這件事。黑魔法師消失太久了，人們早已習慣和平，不知道危機來臨時該怎麼辦。再這樣下去，世界很快就會陷入危險。",
    "所以年幼女皇決定在危機浮上檯面之前挺身而出，組成一支阻止黑魔法師完全復活的騎士團。你會自願成為騎士，應該也知道接下來的事了。",
    "我們的職責很簡單：讓自己變得比現在強大得多。等黑魔法師歸來時，我們要與他一戰，在他讓整個世界陷入危機之前徹底消滅他。這是我們的目標與使命，也是你的使命。",
}
for i, line in ipairs(lines) do
    npc:say(line, i > 1, true)
end
if not npc:ask_accept("以上就是目前的大致情況。明白了嗎？") then
    npc:say("還有不清楚的地方就再來找我吧。", false, false)
    return
end
npc:exchange({ quests = { { id = 20001, state = 1 }, { id = 20001, state = 2 } } })
local after = {
    "很高興你理解我說的話，但是…你知道嗎？以你現在的等級，根本無法面對黑魔法師。別說黑魔法師了，連他手下的手下的寵物的稻草人都打不贏！這樣真的能守護楓之谷世界嗎？",
    "你雖然是皇家騎士團的一員，但還稱不上是騎士。別說正式騎士了，你連見習騎士都還不是。接下來你會有很長一段時間在這裡處理騎士團的文書工作，不過…",
    "不過，沒有人天生就很強。比起尋找天賦異稟的騎士，女皇更希望打造一個能培育出一批批強大騎士的環境。現在你得先成為見習騎士，讓自己變強，日後才能派上用場。等你達到那個程度，我們再來談皇家騎士團的職責。",
    "從左邊的傳送點一直走，就會到#b修煉森林 I#k。在那裡可以找到騎士團的修煉教官奇酷。下次見面時，希望你至少已經10級了。",
}
for i, line in ipairs(after) do
    npc:say(line, i > 1, i < #after)
end
