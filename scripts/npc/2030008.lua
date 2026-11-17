-- 阿杜比斯受理殘暴炎魔的廢礦調查。
if npc:map_id() ~= 211042300 then return end
if npc:level() < 50 then
    npc:say("請先達到 50 級，再來挑戰殘暴炎魔的試煉。", false, false)
    return
end

local permit = 1800100200
local progress = npc:quest_data(permit)
if progress == "" then
    if npc:exchange({quest_data = {{id = permit, data = "1", expected = ""}}}).ok then
        npc:say("在挑戰殘暴炎魔之前，你必須先通過我交給你的試煉。", false, false)
    end
    return
end
if progress ~= "1" and progress ~= "2" then return end

local options = {{id = 0, text = "調查廢礦"}}
local selection = npc:menu("你要繼續哪一項試煉？", options)
if selection == 0 then
    local result = npc:event_start("ZakumMine")
    if not result.ok then
        npc:say("請讓 50 級以上的隊員在此集合，由隊長申請入場。請確認廢礦目前沒有其他隊伍。", false, false)
    end
end
