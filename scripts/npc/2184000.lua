-- 希拉遠征隊：候場、進場和離場；關卡與怪物使用 v148 場景資料。
local name = "Hilla"
local status = npc:event_status(name)
if status.inside then
    if npc:ask_yes_no("確定要離開希拉遠征嗎？離場後將無法回到這次遠征。") then
        npc:event_leave()
    end
    return
end

if npc:map_id() ~= 262030000 then
    return
end

if not npc:ask_yes_no("要挑戰希拉嗎？請先在遠征隊介面建立希拉遠征隊。隊長與隊員共 2 至 6 人，請全員到齊後由隊長申請進場。") then
    return
end

local result = npc:event_start(name)
if result.ok then
    return
end

local answers = {
    unavailable = "目前無法進場，請稍後再試。",
    no_expedition = "請先在遠征隊介面建立希拉遠征隊，或接受隊長的邀請。",
    expedition_type = "你所在的遠征隊不是希拉遠征隊。",
    not_leader = "請由遠征隊隊長申請進場。",
    party_size = "遠征隊需要 2 至 6 人全部到場。目前有 " .. result.here .. " 人到場。",
    roster_absent = "仍有隊員未到場、尚未復活或正在交易。請等全員就緒。",
    level = "隊員等級必須介於 120 至 200 級。",
    busy = "已經有其他遠征隊正在挑戰，請稍後再試。",
}
npc:say(answers[result.reason] or "目前無法挑戰希拉。", false, false)
