local status = npc:amoria_stage()
if status ~= "" then
    npc:say(status, false, false)
end
