
local outcome = npc:kenta_finish()
if outcome == "reward" then
    npc:say("謝謝你們幫忙調查。記得撿取海怒斯掉落的鱗片。", false, false)
    npc:warp(923040000, 0)
elseif outcome == "inventory" then
    npc:say("請先保留一格其他欄位，再找我領獎。", false, false)
elseif outcome == "boss" then
    npc:say("請擊敗洞穴裡的海怒斯。", false, false)
elseif outcome == "started" then
    npc:say("請守護坎特三分鐘。", false, false)
elseif outcome == "defend" then
    npc:say("請守護坎特三分鐘，之後由隊長和我交談。", false, false)
end
