-- 阿杜比斯在普通祭壇前的前置任務與火焰之眼交付。
if npc:map_id() ~= 211042300 then return end
if npc:level() < 50 then
    npc:say("請先達到 50 級，再來挑戰殘暴炎魔的試煉。", false, false)
    return
end

local permit, refining = 1800100200, 1800100201
local ore, breath, wolfTail, eye = 4031061, 4031062, 4000051, 4001017
local progress = npc:quest_data(permit)
if progress == "" then
    if npc:exchange({quest_data = {{id = permit, data = "1", expected = ""}}}).ok then
        npc:say("在挑戰殘暴炎魔之前，你必須先通過我交給你的試煉。", false, false)
    end
    return
end
if progress ~= "1" and progress ~= "2" then return end

local options = {{id = 0, text = "調查廢礦"}}
if npc:item_count(ore) > 0 then
    if npc:item_count(breath) == 0 then
        options[#options + 1] = {id = 1, text = "挑戰火山的氣息"}
    else
        options[#options + 1] = {id = 2, text = "冶煉火焰之眼"}
    end
end
local selection = npc:menu("你要繼續哪一項試煉？", options)
if selection == 0 then
    local result = npc:event_start("ZakumMine")
    if not result.ok then
        npc:say("請讓 50 級以上的隊員在此集合，由隊長申請入場。請確認廢礦目前沒有其他隊伍。", false, false)
    end
elseif selection == 1 and npc:ask_yes_no("你準備好通過火山的氣息了嗎？") then
    npc:warp(280020000, 0)
elseif selection == 2 then
    local refiningProgress = npc:quest_data(refining)
    if npc:item_count(wolfTail) < 30 then
        if refiningProgress == "" then
            npc:exchange({quest_data = {{id = refining, data = "1", expected = ""}}})
        end
        npc:say("再帶來 30 個 #t4000051#，我就能替你冶煉火焰之眼。", false, false)
    elseif npc:ask_yes_no("要交出 #t4031061#、#t4031062# 和 30 個 #t4000051#，換取 5 個 #t4001017# 嗎？") then
        local result = npc:exchange({
            items = {{id = ore, quantity = -1}, {id = breath, quantity = -1},
                     {id = wolfTail, quantity = -30}, {id = eye, quantity = 5}},
            quest_data = {{id = permit, data = "2", expected = progress},
                          {id = refining, data = "2", expected = refiningProgress}},
        })
        if result.ok then
            npc:say("火焰之眼已冶煉完成。你可以帶著它前往殘暴炎魔的祭壇。", false, false)
        else
            npc:say("交付沒有完成。請確認材料數量和消耗欄位。", false, false)
        end
    end
end
