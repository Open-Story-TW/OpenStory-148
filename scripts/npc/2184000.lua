-- 希拉的兩種遠征使用同一個 v148 遠征隊類型。
if npc:event_status("Hilla").inside or npc:event_status("HillaHard").inside then
    if npc:ask_yes_no("確定要離開希拉遠征嗎？離場後將無法回到這次遠征。") then
        npc:event_leave()
    end
    return
end

if npc:map_id() ~= 262030000 then return end

local choice = npc:menu("請先建立希拉遠征隊。2 至 6 人全員到場後，再由隊長選擇難度。", {
    { id = 0, text = "普通希拉（120 至 200 級）" },
    { id = 1, text = "困難希拉（170 至 200 級）" },
})
if choice == nil then return end
local hard = choice == 1
local result = npc:event_start(hard and "HillaHard" or "Hilla")
if result.ok then return end

local reasons = {
    no_expedition = "請先建立希拉遠征隊，或接受隊長的邀請。",
    expedition_type = "你所在的遠征隊並非希拉遠征隊。",
    not_leader = "只有遠征隊長可以申請進入。",
    party_size = "需要 2 至 6 人。現在有 " .. result.here .. " 人到場。",
    roster_absent = "請等遠征隊員全員到齊並準備好，再挑戰。",
    level = hard and "困難遠征需要 170 至 200 級。" or "普通遠征需要 120 至 200 級。",
    busy = "目前有隊伍正在挑戰這個難度。",
}
npc:say(reasons[result.reason] or "目前無法挑戰希拉。", false, false)
