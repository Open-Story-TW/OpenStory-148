-- v148 q3862e, the first Sunburst at the Test Door.
if npc:map_id() ~= 252020700 or npc:quest_state(3862) ~= 1 then return end
if npc:item_count(4033176) < 1 then return end
npc:quest_complete(3862)
npc:warp(252030000, 3)
