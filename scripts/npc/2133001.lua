-- Legacy Ellin stage guide, maze and capture turn-ins.
local map = npc:map_id()
if map == 930000700 then
    npc:warp(930000800, 0)
    return
end
local status = npc:ellin_stage()
if status == "moved" then return end
local messages = {
    [930000000] = "請與隊友前往下一個傳送點。",
    [930000100] = "請清除這裡所有受污染的怪物。",
    [930000200] = "帶著 #t4001161# 點擊岩石稀釋毒液，再帶著 #t4001162# 點擊荊棘四次。",
    [930000300] = "請找到迷宮的出口，再請隊長來找我。",
    [930000400] = "把怪物的生命值降到七成以下，用 #t2270004# 淨化牠們，隊長要交回 20 個 #t4001169#。",
    [930000600] = "帶著 #t4001163# 點擊祭壇，召喚毒石巨人。",
}
if messages[map] then npc:say(messages[map], false, false) end
