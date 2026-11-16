-- Source: Legacy scripts/npc/2040034.js and TWMS v148 Quest.wz/1202.
local choice = npc:menu("#e時空的裂縫#n\r\n請組成 3 到 6 人的隊伍，所有成員需達 50 級。挑戰時間為 20 分鐘。", {
    { id = 0, text = "我要挑戰時空的裂縫" },
    { id = 1, text = "領取破碎的眼鏡" },
})
if choice == 1 then
    local completed = npc:log("character", "permanent", "ludibrium-clear")
    local claims = npc:log("character", "permanent", "ludibrium-glasses")
    local needed = 20 * (claims + 1) - completed
    if needed > 0 then
        npc:say("再完成 #b" .. needed .. "#k 次，就可以領取 #t1022073#。", false, false)
    elseif not npc:can_hold(1022073, 1) then
        npc:say("請先確認裝備欄位是否可以收下 #t1022073#。", false, false)
    elseif npc:exchange({
        items = { { id = 1022073, quantity = 1 } },
        logs = { { scope = "character", period = "permanent", name = "ludibrium-glasses", delta = 1, expected = claims } },
    }).ok then
        npc:say("謝謝你幫忙擊退裂縫中的怪物。這是你的 #b#t1022073##k。", false, false)
    end
    return
end

local result = npc:event_start("LudiPQ")
if result.ok then
    return
end
local reasons = {
    no_party = "請先組隊，再由隊長來找我。",
    not_leader = "請讓你們的隊長來找我。",
    party_size = "需要 3 至 6 名隊員在場，目前這裡有 " .. result.here .. " 人。",
    busy = "已有另一隊在挑戰，請稍候或更換頻道。",
    level = "每名隊員都需要達到 50 級。",
}
npc:say(reasons[result.reason] or "目前無法進入時空的裂縫。", false, false)
