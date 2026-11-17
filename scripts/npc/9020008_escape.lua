local map = npc:map_id()
if map == 921160700 then
    local result = npc:escape_finish()
    if result == "completed" then
        npc:say("謝謝你們救了我！請收下 #b#t4001534##k。", false, true)
        npc:event_leave()
    elseif result == "boss" then
        npc:say("要先打倒教官亞尼，才能讓大家重獲自由。", false, false)
    elseif result == "inventory" then
        npc:say("請先空出一格其他欄位。", false, false)
    end
    return
end
local hints = {
    [921160100] = "避開陷阱，從塔頂的出口離開。",
    [921160200] = "打倒所有守衛，才可以通過右邊的門。",
    [921160400] = "打倒所有守衛，打開通往下一關的門。",
    [921160500] = "避開最後的陷阱，前往塔頂。",
    [921160600] = "打倒守衛取得監獄鑰匙，再雙擊四扇門。每扇門需要一把鑰匙。",
}
if map >= 921160100 and map <= 921160600 then
    local choice = npc:menu(hints[map] or "黑暗之塔的光通往不同房間。想辦法找到通往空中監獄之門。", {
        {id = 0, text = "繼續前進"},
        {id = 1, text = "離開隱藏之塔"},
    })
    if choice == 1 and npc:ask_yes_no("確定要回到隱藏之塔入口嗎？") then
        npc:event_leave()
    end
end
