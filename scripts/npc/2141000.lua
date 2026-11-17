-- 奇勒斯坦和召喚皮卡啾的祭壇。
if npc:event_status("PinkBean").inside and npc:ask_yes_no("要交出一顆混沌之球，啟動雕像祭壇嗎？") then
    if not npc:event_summon() then
        npc:say("請準備一顆混沌之球，並等遠征隊員就緒。", false, false)
    end
end
