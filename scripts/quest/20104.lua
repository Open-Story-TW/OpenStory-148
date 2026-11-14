-- Reviewed conversion of Legacy scripts/quest/20104.js (暗夜行者之路, end at 騎士團教官).
-- Original SHA256: e3f5bd15cb8b698e358a2b1c8bd10cc2931db7df996f50e5d216067f7473a5e5.
-- changeJob, its items, bag growth and the completion of this quest are one
-- server transaction (internal/persistence/quest_jobs.go); this source only
-- asks and names the job; English Legacy lines are given in Chinese.
if npc:quest_state(20104) ~= 1 then return end
if not npc:ask_yes_no("你決定好了嗎？這個決定無法反悔，請仔細考慮。你確定要成為#b暗夜行者#k嗎？") then
    npc:say("這是很重要的決定，想清楚了再來找我吧。", false, false)
    return
end
if not npc:advance_first_job(1400).ok then
    npc:say("請確認裝備欄與消耗欄有空位，並且已經符合轉職條件。", false, false)
    return
end
npc:say("我已經把你的身體鍛鍊成最適合暗夜行者的狀態了。想要變得更強，就打開能力值視窗（S）分配能力值吧。不知道該加什麼的話，按下#b自動分配#k就可以了。", false, true)
npc:say("我也擴充了你的裝備欄和其他欄的格數。好好利用這些空間，裝滿騎士需要的道具吧。", true, true)
npc:say("另外也給了你一點#bSP#k，打開#b技能視窗#k學習新技能吧。技能無法一次全部提升，有些技能也要先學會基礎技能才能學習。", true, true)
npc:say("和還是貴族的時候不同，成為暗夜行者之後，HP耗盡時會損失一部分經驗值，要小心喔。", true, true)
npc:say("那麼…去向全世界證明皇家騎士團的實力吧！", true, false)
