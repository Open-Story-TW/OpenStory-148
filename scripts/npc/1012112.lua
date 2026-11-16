-- Source: Legacy scripts/npc/1012112.js and TWMS v148 Quest.wz/1200.
-- Source SHA256: 4fbb7037249600b8595b87130b67ec4daf82e3d6eb0f997830d6d89cca4a06d9
local LOBBY, CAKE = 910010500, 4001101
local FIRST_HAT, SECOND_HAT = 1003418, 1003419

if npc:map_id() ~= LOBBY then
    if npc:ask_yes_no("要前往#b月妙的年糕#k等候室嗎？") then
        npc:warp(LOBBY, 0)
    end
    return
end

local choice = npc:menu("想和夥伴一起幫助月妙嗎？請組成 3 到 6 人的隊伍，所有成員需達 50 級。", {
    { id = 0, text = "我要挑戰月妙的年糕" },
    { id = 1, text = "用元宵交換年糕帽" },
})
if choice == 1 then
    local clear_count = npc:log("character", "permanent", "henesys-clear")
    if clear_count < 1 then
        npc:say("先完成一次#b月妙的年糕#k，再來交換吧！", false, false)
        return
    end

    local first = npc:log("character", "permanent", "henesys-hat-one")
    local hat, count, log_name = FIRST_HAT, 10, "henesys-hat-one"
    if first > 0 then
        hat, count, log_name = SECOND_HAT, 20, "henesys-hat-two"
    end
    if npc:log("character", "permanent", log_name) > 0 then
        npc:say("兩頂年糕帽都已經送給你了。", false, false)
    elseif npc:item_count(CAKE) < count then
        npc:say("帶 #b" .. count .. "#k 個 #t4001101# 回來，就可以交換 #t" .. hat .. "#。", false, false)
    elseif not npc:can_hold(hat, 1) then
        npc:say("請先空出裝備欄位。", false, false)
    elseif npc:exchange({
        items = { { id = CAKE, quantity = -count }, { id = hat, quantity = 1 } },
        logs = { { scope = "character", period = "permanent", name = log_name, delta = 1, maximum = 1 } },
    }).ok then
        npc:say("這是送給你的 #b#t" .. hat .. "##k。", false, false)
    else
        npc:say("交換沒有完成，請確認背包後再試。", false, false)
    end
    return
end

local result = npc:event_start("HenesysPQ")
if result.ok then
    return
end
local answers = {
    unavailable = "現在無法進場，請稍後再試。",
    no_party = "請先組隊，再由隊長來找我。",
    not_leader = "請讓你們的隊長來找我。",
    busy = "另一隊正在挑戰，請稍候或更換頻道。",
    party_size = "等候室裡需要有 3 至 6 名隊員，目前有 " .. result.here .. " 人。",
    level = "每位隊員都要達到 50 級。",
}
npc:say(answers[result.reason] or answers.unavailable, false, false)
