local map = npc:map_id()
if map == 889100100 then
    local mode = npc:menu("和隊友一起保護雪巨人吧！", {
        {id = 0, text = "簡單，等級 10 以上"},
        {id = 1, text = "普通，等級 30 以上"},
        {id = 2, text = "困難，等級 70 以上"}
    })
    if mode ~= nil then
        npc:warp(889100000 + 10 * mode, 0)
    end
    return
end
local mode = math.floor((map - 889100000) / 10)
if mode < 0 or mode > 2 then return end
local lobby = 889100000 + 10 * mode
if map == lobby then
    if not npc:ask_yes_no("要帶隊挑戰保護雪巨人嗎？請至少兩人一起進場。") then return end
    local entry = npc:event_start("Christmas" .. mode)
    if not entry.ok then
        npc:say("請確認隊長、至少兩名合資格隊員都在這裡，而且目前沒有其他隊伍在挑戰。", false, false)
    end
elseif map == lobby + 1 then
    local result = npc:christmas_finish()
    if result == "leader" then
        npc:say("請隊長來和我說話。", false, false)
    elseif result == "defend" then
        npc:say("請保護雪巨人，直到倒數結束！", false, false)
    elseif result == "boss" then
        npc:say("請擊敗鬼精靈！", false, false)
    end
end
