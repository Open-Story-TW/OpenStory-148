-- Reviewed conversion of Legacy scripts/quest/21101.js (使用矛的英雄, start at the polearm).
-- Original SHA256: 7112c02e7e890847bee24b9d62b2df42f64fcccd7760167c2ce1970e7de59326.
-- changeJob(2100), resetStats(35,4,4,4), bags, the medal and completing 21101
-- and 29924 are one server transaction (internal/persistence/quest_jobs.go).
-- Legacy then played the memory scene (MovieClipIntroUI, map 914090100); that
-- map has no direction script here, so the character stays where it is.
-- teachSkill(…, 0) only showed skills the character can already learn with SP.
if npc:quest_state(21101) ~= 0 or npc:quest_state(21100) ~= 2 then return end
if not npc:ask_yes_no("#b(我自己確信是使用過 #p1201001#的英雄嗎？ 確定的話就拿出力量抓住 #p1201001#吧 一定會有什麼反應。)#k") then
    npc:say("#b(再考慮一下好了...)#k", false, false)
    return
end
if not npc:advance_first_job(2100).ok then
    npc:say("請確認裝備欄有空位，並且已經符合轉職條件。", false, false)
    return
end
npc:say("#b(好像想起什麼了...)#k", false, false, 3)
