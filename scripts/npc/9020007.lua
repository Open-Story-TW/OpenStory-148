
local map = npc:map_id()
if map == 923040100 then
    npc:say("擊退海底所有威脅坎特的怪物，由隊長帶大家從上方傳點離開。", false, false)
elseif map == 923040200 then
    npc:say("全隊收集怪物掉落的 20 個氣泡後，由隊長帶大家前往下一個區域。", false, false)
else
    npc:say("保護坎特三分鐘。防衛成功後，請隊長和坎特交談。", false, false)
end
