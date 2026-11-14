-- Phantom tutorial, the bedroom of the airship (map 915000000, onUserEnter
-- PTtutor000). Phantom paces the room, thinks aloud and is then free to leave
-- through the door on the left.
scene:direct("alone", 1)
scene:direct("lock", 1)
scene:direct("screen", "phantom/mapname1")
for _, step in ipairs({ { 1, 1000 }, { 0, 1000 }, { 2, 1000 }, { 0, 1000 }, { 1, 1000 }, { 0, 1000 }, { 2, 1000 }, { 0, 1000 }, { 1, 500 }, { 0, 1000 } }) do
    scene:direct("walk", step[1])
    scene:wait(step[2])
end
scene:say_as("決定的時刻終於到了嗎？", 0)
scene:say_as("還真有點緊張。是因為太久沒活動了嗎？當然，並不是沒有自信。", 0, true)
scene:say_as("準備應該都完成了吧？要是再拖下去錯過了時機，那可就太丟臉了。雖然還有點生疏，不過就去試試看吧。", 0, true)
scene:direct("lock", 0)
