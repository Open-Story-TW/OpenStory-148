-- 僧侶瑪妮在祭壇入口與祭壇。
local map = npc:map_id()
if map == 252030000 then
    if npc:quest_state(3863) == 1 then
        npc:say("請從旁邊的傳點進入祭壇，擊倒受了致命傷的六手邪神。", false, false)
    else
        npc:say("準備好時，請和夥伴在這裡集合，由隊長從旁邊的傳點進入祭壇。", false, false)
    end
elseif npc:ask_yes_no("要離開黃金祭壇嗎？請先拾取地上的戰利品。") then
    npc:event_leave()
end
