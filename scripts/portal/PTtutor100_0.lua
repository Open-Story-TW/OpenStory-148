-- Reviewed source: the hall's exit (map 915000100) leads outside the airship
-- once 阿弗雷德 has started quest 25000 潛入作戰.
if portal:quest_state(25000) == 0 then
    portal:message("請先和管家阿弗雷德對話。")
    return
end
portal:warp(915000200, 2)
