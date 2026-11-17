-- 炎魔遠征隊入口：難度依所在候場地圖而定。
local field = npc:map_id()
if field ~= 211042400 and field ~= 211042401 then
    return
end
local chaos = field == 211042401
local name = chaos and "混沌殘暴炎魔" or "殘暴炎魔"
if not npc:ask_yes_no("要挑戰" .. name .. "嗎？請讓符合資格的遠征隊員全員到齊，再由隊長申請。") then
    return
end
local response = npc:event_start(chaos and "ChaosZakum" or "Zakum")
if response.ok then
    return
end
local limits = chaos and "5 至 30 人（100 至 200 級）" or "5 至 30 人（50 至 200 級）"
local message = {
    no_expedition = "請先在遠征介面建立" .. name .. "遠征隊。",
    expedition_type = "遠征隊的難度和這個入口不符。",
    party_size = "需要 " .. limits .. "。目前有 " .. response.here .. " 人到場。",
    level = "遠征隊須有 " .. limits .. "。",
    roster_absent = "請等所有隊員到齊後再進場。",
    not_leader = "請由遠征隊隊長申請進場。",
    busy = "已有遠征隊正在挑戰。",
    zakum_prerequisite = "每位隊員都須完成前置任務，並持有火焰之眼。",
}
npc:say(message[response.reason] or "目前無法進場。", false, false)
