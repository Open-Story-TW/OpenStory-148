-- Source: Legacy scripts/npc/2100.js
-- Source SHA256: e708d9c1300664dd041310d2110856070270ae6f835b489a5f34a3f919628f1a
-- Sequential conversion. Legacy target maps 1/40000 are preserved; the host
-- validates availability instead of silently rewriting the destination.
local map = npc:map_id()
if map == 0 or map == 3 then
    if npc:ask_yes_no("歡迎來到晴空谷. 這裡是新手地圖. 您想要進行新手教學嗎?") then
        npc:say("教學即將開始,請追隨您的指導員吧.")
        npc:warp(1, 0)
    elseif npc:ask_yes_no("您確定要跳過教學嗎?") then
        npc:say("好ㄉ,助您旅途愉快")
        npc:warp(40000, 0)
    else
        npc:say("等您決定後在跟我說吧.")
    end
else
    while true do
        npc:say("這是您的第一個訓練. 你可以在這邊看到各職業的展示.")
        if npc:say("等到時機成熟,您可自行挑一個職業", true, false) then
            break
        end
    end
end
