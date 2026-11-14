-- Reviewed source for v148 quest 2638 影武者之路2 (q2638s and q2638e at 雪姬).
-- Legacy is v145 and has no script for this quest; the lines follow the v148
-- quest text. The advancement, taking the 30 黑珠子 and finishing this
-- quest are one server transaction modelled on Legacy q2369e
-- (internal/persistence/quest_jobs.go); this source only asks.
local state = npc:quest_state(2638)
if state == 0 then
    if npc:quest_state(2637) ~= 2 then return end
    npc:say("擁有慧眼的人啊，你應該踏上的旅程，就是影武者之路。", false, true)
    npc:say("從#b影之刃#k那裡把#b#t4031013# 30個#k帶回來。只要做得到，我就承認你的力量，開啟你的慧眼，讓你覺醒成擁有更強大力量的雙刀流影武者。", true, true)
    if not npc:ask_accept("你願意接受這個考驗嗎？") then
        npc:say("還沒準備好的話，就再來找我吧。", false, false)
        return
    end
    npc:quest_start(2638)
    npc:say("我會等你帶著#b#t4031013##k回來。", false, false)
    return
end
if state ~= 1 then return end
if npc:item_count(4031013) < 30 then
    npc:say("還沒收集到#b#t4031013# 30個#k嗎？要從#b影之刃#k那裡帶回來。", false, false)
    return
end
if not npc:ask_yes_no("你帶回了#b#t4031013# 30個#k。現在就覺醒為#b影武者#k嗎？") then
    npc:say("想清楚了再來找我吧。", false, false)
    return
end
if not npc:advance_first_job(431).ok then
    npc:say("請確認已經符合轉職條件。", false, false)
    return
end
npc:say("你已經覺醒為#b影武者#k了。這股強大的力量能走到哪裡，就看你自己了。到了55級我會再找你。", false, false)
