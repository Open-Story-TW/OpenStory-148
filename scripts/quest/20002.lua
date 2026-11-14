-- Reviewed conversion of Legacy scripts/quest/20002.js (修煉教官奇酷).
-- Original SHA256: 6654aebd7b712b213e1b4e3efbdba85e3a40ff4d2cba8aa49423420cb9afefbe.
-- Legacy stepped back one line on a decline and looped until ESC; here a
-- decline ends the talk. The English Legacy lines are given in Chinese.
-- Check.img: 20001 finished and none of the Kimu tutorial quests started.
if npc:quest_state(20002) ~= 0 or npc:quest_state(20001) ~= 2 then return end
for id = 20010, 20020 do
    if npc:quest_state(id) ~= 0 then return end
end
npc:say("什麼？是那因哈特派你來的？啊，你一定是新來的吧。歡迎歡迎，我叫奇酷，負責把你們這些孩子訓練成真正的騎士。嗯…為什麼這樣看著我…啊，你一定是第一次看到皮妖吧。")
npc:say("我們是叫做皮妖的種族。你跟女皇身旁的神獸說過話了吧？神獸也是皮妖，雖然種類不太一樣就是了。皮妖只住在耶雷弗，一開始可能會覺得我們有點奇怪，很快就會習慣的。")
if not npc:ask_accept("啊，不知道你知不知道，耶雷弗是沒有怪物的。任何邪惡的東西都無法踏上這座島。不過別擔心，你還是可以在這裡修煉。神獸創造了叫做「提提」的幻想生物，牠們就是你的修煉對象。那麼開始吧？") then
    return
end
local result = npc:exchange({
    exp = 60,
    items = { { id = 2000020, quantity = 10 }, { id = 2000021, quantity = 10 }, { id = 1002869, quantity = 1 } },
    quests = { { id = 20002, state = 1 }, { id = 20002, state = 2 } },
})
if not result.ok then
    npc:say("請確認道具欄有足夠的空間後再來找我。", false, false)
    return
end
npc:say("哈，我喜歡你的幹勁，不過開始之前要先做好準備。確認你已經裝備好武器，技能也準備好可以使用了。我也給了你一些藥水，以防萬一。準備好了就告訴我吧。你會後悔加入皇家騎士團的，哈哈。", false, false)
