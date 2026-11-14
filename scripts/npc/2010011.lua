-- Source: Legacy scripts/npc/2010011.js (蕾雅)
-- Source SHA256: b4337bd8f1220b70a44506db24fcf24e6f4835de63e812ba4f7107161787c70a
-- Legacy cm.warp(200000301) picked a random portal of the guild headquarters;
-- this arrives at its spawn point (portal 0).
if npc:ask_yes_no("您要去 #b公會本部(英雄之殿)#k 嗎?") then
    npc:warp(200000301, 0)
end
