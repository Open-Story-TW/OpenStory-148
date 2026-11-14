-- Phantom tutorial: on the way to the conference hall (map 915000400).
if scene:quest_data(25003) == "2" then return end
scene:say_as("哎呀……已經開始了嗎？就不能稍微晚一點嗎？", 0)
scene:exchange({ quest_data = { { id = 25003, data = "2" } } })
