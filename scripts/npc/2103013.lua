-- 杜亞特，奈特的金字塔。
local lobby, exit = 926010000, 926020001
local map = npc:map_id()
if map == exit then
    local reward = npc:log("character", "permanent", "nett_defense_emeralds")
    if reward > 0 then
        if not npc:ask_yes_no("你有 " .. reward .. " 顆 #b#t4001623##k 可以領取。現在要領取嗎？") then
            return
        end
        local outcome = npc:exchange({
            items = {{id = 4001623, quantity = reward}},
            logs = {{scope = "character", period = "permanent", name = "nett_defense_emeralds",
                     expected = reward, delta = -reward}},
        })
        if outcome.ok then
            npc:say("你已領取 #b#t4001623##k。", false, false)
        else
            npc:say("請確認其他欄有足夠的背包空間。", false, false)
        end
        return
    end
    if npc:ask_yes_no("要回到金字塔山丘嗎？") then npc:warp(lobby, 4) end
    return
end

if map ~= lobby then
    if npc:ask_yes_no("確定要離開奈特的金字塔？") then npc:event_leave() end
    return
end

local mode = npc:menu("四名同伴守住方尖塔的四層通道。每人每天可以挑戰五次。", {
    {id = 1, text = "一般模式（60～89 級）"},
    {id = 2, text = "困難模式（90～109 級）"},
})
if mode ~= 1 and mode ~= 2 then return end
local entry = npc:event_start(mode == 1 and "NettNormal" or "NettHard")
if entry.ok then return end
local messages = {
    no_party = "請先組成四人隊伍。",
    not_leader = "請由隊長報名。",
    party_size = "請讓四名隊員都到金字塔山丘。",
    level = "每位隊員都必須符合所選模式的等級限制。",
    daily = "有隊員已達今日挑戰上限。",
    busy = "有人正在挑戰這個模式，請稍後再試。",
}
npc:say(messages[entry.reason] or "目前無法進入奈特的金字塔。", false, false)
