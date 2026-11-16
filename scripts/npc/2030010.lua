-- 祭壇嚮導。領取戰利品後可從這裡回到神殿。
local field = npc:map_id()
if (field == 280030000 or field == 280030001) and npc:ask_yes_no("確定要離開祭壇嗎？離場後將無法回到這次遠征。") then
    npc:event_leave()
end
