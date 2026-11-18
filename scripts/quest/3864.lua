-- v148 q3864e, the repeatable Sunburst at the Test Door.
if npc:map_id() ~= 252020700 or npc:quest_state(3864) ~= 1 then return end
if npc:item_count(4001684) < 1 then return end
if not npc:ask_yes_no("要交出一個太陽火花，前往六手邪神的祭壇入口嗎？") then return end
npc:quest_complete(3864)
npc:warp(252030000, 3)
