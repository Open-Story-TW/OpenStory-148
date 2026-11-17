-- Sleepywood statue and the three original John flower quests.
if npc:map_id() ~= 105000000 then return end
local courses = {
    {quest = 2054, flower = 4031028, map = 910530200},
    {quest = 2053, flower = 4031026, map = 910530100},
    {quest = 2052, flower = 4031025, map = 910530000},
}
local active, completed
for _, course in ipairs(courses) do
    local state = npc:quest_state(course.quest)
    if state == 1 and not active then active = course end
    if state == 2 and not completed then completed = course end
end
local course = active or completed
if not course then
    npc:say("請先向約翰接取採花任務。", false, false)
    return
end
-- The WZ confirms possession of the flowers but does not consume them.
-- Flowers from an earlier quest cannot block the next course.
if npc:item_count(course.flower) > 0 then
    npc:say("雕像沒有反應。你身上已經帶著這一關的 #b#t" .. course.flower .. "##k。", false, false)
    return
end
if npc:ask_yes_no("雕像發出奇異的光芒。你要進入森林嗎？") then
    npc:warp(course.map, 0)
end
