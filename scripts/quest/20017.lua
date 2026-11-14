-- Reviewed conversion of Legacy scripts/quest/20017.js (成為騎士的第一次修煉).
-- Original SHA256: 5d36260b5d6a60f31e3f22d55e7b4ef3f6b9fb098dd75fa8e1cdb0ca3d203132.
-- Legacy stepped back one line on a decline and looped until ESC; here a
-- decline ends the talk. summonMsg tutorial hints show nothing here.
if npc:quest_state(20017) ~= 0 or npc:quest_state(20016) ~= 2 then return end
npc:say("嗯？是#p1101002#派你來的？啊哈！看來是這次新來的騎士嘛！歡迎，歡迎，我叫#p1102000#，是教導你們這些貴族的修煉教官。哦…為什麼這樣看我…啊，看來你是第一次看到皮妖吧。")
npc:say("我們是叫做皮妖的種族。你和小女皇旁邊的#p1101001#說過話嗎？皮妖和#p1101001#是同一個種族，雖然系列不同…但基本上差不多。我們只生活在耶雷弗，你很快就會習慣的。", true, true)
npc:say("啊，你知道嗎？耶雷弗裡沒有怪物，帶有邪惡氣息的東西無法在耶雷弗生存。不過不用擔心，用#p1101001#製造的幻想生物「提提」修煉就可以了，那麼開始吧。", true, true)
if not npc:ask_accept("修煉得還不錯嘛！那麼…看你的程度，應該能消滅比較強的提提了。去消滅#m130010100#裡的#o0100122# 15隻吧，怎麼樣？能抓到#o0100122#吧？") then
    npc:say("嗯，沒什麼好擔心的。以你的程度一定輕而易舉。鼓起勇氣，準備好了再告訴我吧。", false, false)
    return
end
npc:exchange({ quests = { { id = 20003, state = 2 }, { id = 20017, state = 1 } } })
