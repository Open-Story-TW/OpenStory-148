-- Reviewed source for v148 quest 2642 上忍之路2 (q2642e at 雪姬; the start
-- needs no script). Legacy is v145 and has no script for this quest; the lines
-- follow the v148 quest text. The advancement, its reward and finishing this
-- quest are one server transaction modelled on Legacy q2374e, which also
-- checks the 99 冰龍 (internal/persistence/quest_jobs.go).
if npc:quest_state(2642) ~= 1 then return end
if not npc:ask_yes_no("你擊退了#b冰龍 99隻#k，證明了雙刀流的力量。現在就踏上#b上忍#k之路嗎？") then
    npc:say("想清楚了再來找我吧。", false, false)
    return
end
if not npc:advance_first_job(432).ok then
    npc:say("冰龍… 是很難對付的對象嗎？請確認已經擊退#b冰龍 99隻#k，並且裝備欄有空位。", false, false)
    return
end
npc:say("從現在起，你就是#b上忍#k了。雙刀流的命脈，就交給你繼續延續下去。", false, false)
