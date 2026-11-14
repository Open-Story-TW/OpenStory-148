-- Phantom tutorial: the portal outside the airship (map 915000200) drops
-- Phantom into the Knights' Hall of Ereve.
if scene:quest_state(25000) ~= 1 then return end
scene:say_as("穿過這個傳送點就是耶雷弗了……那裡到處都是騎士，得小心一點。就讓我發揮一下當年的實力吧，呵呵。", 0)
scene:warp(915000300, 1)
