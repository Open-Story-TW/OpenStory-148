-- 阿卡伊農的分身消失後才能挑戰本體。
if npc:event_status("Arkarium").inside and npc:ask_yes_no("正面挑戰我吧！") then
    if not npc:event_summon() then
        npc:say("請先消滅我的分身，並等遠征隊員就緒。", false, false)
    end
end
