-- 凡雷恩的開戰對話。
if npc:event_status("VonLeon").inside and npc:ask_yes_no("你就是要打敗我的人嗎？那就放馬過來吧！") then
    if not npc:event_summon() then
        npc:say("請等遠征隊員就緒，並留在謁見室內。", false, false)
    end
end
