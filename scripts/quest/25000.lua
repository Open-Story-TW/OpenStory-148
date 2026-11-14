-- Reviewed source for v148 quest 25000 潛入作戰 (q25000s at 管家 阿弗雷德,
-- map 915000100). Legacy has no Phantom tutorial; the lines follow the v148
-- quest text. The quest ends by itself when the tutorial reaches the
-- 水晶花園 (scene/PTjob1.lua).
if npc:quest_state(25000) ~= 0 then return end
npc:say("準備好了嗎，主人？#m150000000#現在就停在#m130000000#的騎士之殿上空。當然，下面的人應該還沒發現。", false, true)
npc:say("不過現在#m130000000#的警備相當森嚴。情況似乎和平常不太一樣……騎士之殿前面有許多警衛正在待命。", true, true)
npc:say("單是避開警衛應該不難。只要照主人的計畫，抓準施展幻影魔法的時機就行了。", true, true)
npc:say("在主人完成行動回來之前，#m150000000#會一直在這裡待命。引擎不會熄火，所以不必擔心被人追蹤。", true, true)
if not npc:ask_accept("一切都準備好了，現在就等主人決定……如何？要現在開始#m130000000#潛入作戰嗎？") then
    npc:say("雖然警備森嚴、能用的技能也有限，但以主人的能力，潛入絕對不是問題。", false, false)
    return
end
npc:quest_start(25000)
npc:say("祝您好運，主人。從大廳中央的出口出去，就能到飛行船外面。", false, false)
