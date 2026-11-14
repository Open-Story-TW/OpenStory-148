-- Source: Legacy scripts/npc/1052001.js
-- Source SHA256: 87fa84dbd760f77d9893c44085b37bfc3894d06865854f4f52f8bf4b83d9294b
-- The baseline uses a generic job advancer at this NPC, not a thief-only path.
-- Reviewed additions: the exact job100, level>=30 -> Spearman130 branch.
-- Other second/third/fourth choices remain pending; no arbitrary job setter.
if npc:job() == 100 and npc:level() >= 30 then
    if not npc:ask_yes_no("Would you like to job advance?") then return end
    local selected = npc:menu("Please, select your job:", {{id = 130, text = "Spearman"}})
    if selected == nil then return end
    if not npc:ask_yes_no("Are you sure you want to Job Advance?") then return end
    local result = npc:advance_second_job(selected)
    if not result.ok then npc:say("You may not advance at the current state.", false, false) end
    return
end
if npc:job() ~= 0 or npc:level() < 10 then
    npc:say("You may not advance at the current state.", false, false)
    return
end
if not npc:ask_yes_no("Would you like to job advance?") then
    return
end
local selected = npc:menu("Please, select your job:", {
    {id = 100, text = "Warrior"},
    {id = 200, text = "Magician"},
    {id = 300, text = "Bowman"},
    {id = 400, text = "Thief"},
    {id = 500, text = "Pirate"}
})
if selected == nil then
    return
end
if not npc:ask_yes_no("Are you sure you want to Job Advance?") then
    return
end
-- This source calls changeJob alone: there is no gift or capacity expansion.
local result = npc:advance_first_job(selected)
if not result.ok then
    npc:say("You may not advance at the current state.", false, false)
end
