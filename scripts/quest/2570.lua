-- Reviewed conversion of Legacy scripts/quest/2570.js (轉職成重砲指揮官, end at 卡伊琳).
-- Original SHA256: 63ab258545e0327eabcf666fcf461f9e74343b34e549c591b1ea3068c5dccc90.
-- changeJob, its items, bag growth and the completion of this quest are one
-- server transaction (internal/persistence/quest_jobs.go); this source only
-- asks and names the job; English Legacy lines are given in Chinese.
if npc:quest_state(2570) ~= 1 then return end
if not npc:ask_yes_no("你決定好了嗎？這個決定無法反悔，請仔細考慮。你確定要成為#b重砲指揮官#k嗎？") then
    npc:say("這是很重要的決定，想清楚了再來找我吧。", false, false)
    return
end
if not npc:advance_first_job(501).ok then
    npc:say("請確認裝備欄有空位，並且已經符合轉職條件。", false, false)
    return
end
npc:say("我已經把你的身體鍛鍊成最適合重砲指揮官的狀態了。想要變得更強，就打開能力值視窗（S）分配能力值吧。不知道該加什麼的話，按下#b自動分配#k就可以了。", false, true)
npc:say("我也擴充了你的裝備欄、消耗欄和其他欄的格數。好好利用這些空間，裝滿冒險家需要的道具吧。", true, true)
npc:say("那麼…去向全世界證明冒險家的實力吧！", true, false)
