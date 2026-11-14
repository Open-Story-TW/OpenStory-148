-- Reviewed conversion of Legacy scripts/quest/23011.js (煉獄巫師之路, end at 反抗軍教官).
-- Original SHA256: 0f7394e6e75db0ecb0056aad5ffb994725de56377ce9cde3899f61d9d02229d8.
-- changeJob, its items, bag growth and the completion of this quest are one
-- server transaction (internal/persistence/quest_jobs.go); this source only
-- asks and names the job; English Legacy lines are given in Chinese.
if npc:quest_state(23011) ~= 1 then return end
if not npc:advance_first_job(3200).ok then
    npc:say("請確認裝備欄有空位，並且已經符合轉職條件。", false, false)
    return
end
npc:say("好！正式歡迎你成為末日反抗軍。從現在開始你是一名煉獄巫師。身為一名懷著狂氣的魔法師，要比任何人搶先一步對抗敵人。", false, true)
npc:say("倘若煉獄巫師的身分曝光大家都知道了，會變的很棘手？如果被黑色翅膀發現的話，會變的麻煩的。那你從現在開始叫我老師吧。你需要特別課程來成為一位能夠獨當一面的反抗軍。", true, false)
