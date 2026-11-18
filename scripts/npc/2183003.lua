-- 阿斯旺避難所最右側的入口。
if npc:map_id() ~= 262000000 then return end

local entries = npc:log("character", "day", "azwan_sweep_entries")
local choice = npc:menu("要前往阿斯旺戰場嗎？", {
    {id = 1, text = "掃蕩希拉殘黨（40 級以上，今日剩餘 " .. math.max(10 - entries, 0) .. " 次）"},
    {id = 2, text = "前往希拉入口（120 級以上）"},
})
if choice == 2 then
    if npc:level() >= 120 then
        npc:warp(262030000, 0)
    else
        npc:say("120 級以上才能前往希拉入口。", false, false)
    end
    return
end
if choice ~= 1 then return end

local result = npc:event_start("AzwanSweep")
if result.ok then return end
local messages = {
    not_leader = "請隊長來報名。",
    party_size = "請讓所有隊員到阿斯旺避難所集合。",
    level = "參與掃蕩的隊員都必須達到 40 級。",
    daily = "有隊員已達今日挑戰上限。",
    busy = "有隊伍正在掃蕩希拉殘黨，請稍後再來。",
}
npc:say(messages[result.reason] or "目前無法前往阿斯旺戰場。", false, false)
