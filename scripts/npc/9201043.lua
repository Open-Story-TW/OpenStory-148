local status = npc:amoria_entry()
if status == "level" then
    npc:say("結婚組隊任務只開放給 40 級以上的已婚玩家。", false, false)
elseif status == "marriage" then
    npc:say("請先登記結婚，才能加入結婚組隊任務。", false, false)
elseif status == "ticket" then
    if npc:ask_yes_no("要使用一張 #t4031592# 進入挑戰入口嗎？") then
        if not npc:amoria_board() then
            npc:say("暫時無法前往挑戰入口。", false, false)
        end
    end
elseif status == "keys" then
    if npc:ask_yes_no("要用 10 個 #t4031593# 兌換一張 #t4031592# 嗎？") then
        local result = npc:amoria_ticket()
        if result == "issued" then
            npc:say("已兌換 #t4031592#。再次與我對話就能進入挑戰入口。", false, false)
        elseif result == "cooldown" then
            npc:say("上次領取入場券後尚未滿六小時。", false, false)
        else
            npc:say("請確認鑰匙數量與其他道具欄的空間。", false, false)
        end
    end
else
    npc:say("請到紫木森林取得 10 個 #t4031593#，我會幫你兌換入場券。", false, false)
end
