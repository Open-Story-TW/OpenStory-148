-- Reviewed conversion of Legacy scripts/quest/23012.js (狂豹獵人之路, end at 反抗軍教官).
-- Original SHA256: 99cda1e73be154083eb3fbdeef238082abd19e0aa89b71c7e9bbe941628ae7b6.
-- changeJob, its items, bag growth and the completion of this quest are one
-- server transaction (internal/persistence/quest_jobs.go); this source only
-- asks and names the job; English Legacy lines are given in Chinese.
if npc:quest_state(23012) ~= 1 then return end
if not npc:ask_yes_no("你決定好了嗎？這個決定無法反悔，請仔細考慮。你確定要成為#b狂豹獵人#k嗎？") then
    npc:say("這是很重要的決定，想清楚了再來找我吧。", false, false)
    return
end
if not npc:advance_first_job(3300).ok then
    npc:say("請確認裝備欄有空位，並且已經符合轉職條件。", false, false)
    return
end
npc:say("好！正式歡迎你成為末日反抗軍。從現在開始你是一名狂豹獵人。", false, true)
npc:say("我也教了你#b美洲豹騎乘#k和#b召喚美洲豹#k的技能，打開技能視窗看看吧。", true, true)
npc:say("我也擴充了你的裝備欄、消耗欄和其他欄的格數。好好利用這些空間，裝滿反抗軍需要的道具吧。", true, true)
npc:say("那麼…去讓大家看看末日反抗軍的實力吧！", true, false)
