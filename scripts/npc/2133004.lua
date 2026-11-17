-- Legacy Ellin basket room.
if npc:map_id() ~= 930000500 then return end
if npc:ellin_stage() ~= "moved" then
    npc:say("找出籃子裡的 #t4001163#，再帶來給我。", false, false)
end
