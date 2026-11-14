-- Reviewed source for v148 quest 2623 下忍 (q2623s and q2623e at 雪姬).
-- Legacy is v145 and has no script for this quest; the lines follow the v148
-- quest text. The advancement, the katara, bag growth, taking the 慧眼 and
-- finishing this quest are one server transaction modelled on Legacy q2363e
-- (internal/persistence/quest_jobs.go); this source only asks.
local state = npc:quest_state(2623)
if state == 0 then
    if npc:quest_state(2622) ~= 2 then return end
    npc:say("要踏上雙刀流之路，需要的是銳利的#b慧眼#k。那是只有具備主人資格的人才能擁有的東西。", false, true)
    npc:say("到秘密花園的#b珠子之房#k，打擊#b混濁的玻璃珠#k，把得到的東西點兩下打碎，就能得到#b#t4032616##k。", true, true)
    if not npc:ask_accept("把#b#t4032616##k帶回來給我。你準備好了嗎？") then
        npc:say("還沒準備好的話，就再來找我吧。", false, false)
        return
    end
    npc:quest_start(2623)
    npc:say("去吧。我會在這裡等你。", false, false)
    return
end
if state ~= 1 then return end
if npc:item_count(4032616) < 1 then
    npc:say("還沒拿到#b#t4032616##k嗎？到#b珠子之房#k打碎#b混濁的玻璃珠#k就能得到。", false, false)
    return
end
if not npc:ask_yes_no("你帶回了#b#t4032616##k。現在就覺醒為影武者的第一個階段，#b下忍#k嗎？") then
    npc:say("想清楚了再來找我吧。", false, false)
    return
end
if not npc:advance_first_job(430).ok then
    npc:say("請確認裝備欄有空位，並且已經符合轉職條件。", false, false)
    return
end
npc:say("從現在起，你就是#b下忍#k了。這把#b#t1342000##k是雙刀流的副武器，裝在盾牌的位置，和短劍一起使用。", false, true)
npc:say("我也擴充了你每個道具欄的格數。好好修練，到了30級我會再找你。", true, false)
