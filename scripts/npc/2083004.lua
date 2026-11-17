-- 闇黑龍王經由生命之穴的洞穴入口登記。
if npc:map_id() ~= 240050400 then return end
local choice = npc:menu("您想挑戰哪一種闇黑龍王？請讓 6 至 30 名遠征隊員到場。", {
    { id = 0, text = "普通闇黑龍王（80 至 200 級）" },
    { id = 1, text = "混沌闇黑龍王（110 至 200 級）" },
})
if choice == nil then return end
local result = npc:event_start(choice == 1 and "ChaosHorntail" or "Horntail")
if result.ok then return end
local why = {
    no_expedition = "請先建立對應的闇黑龍王遠征隊。",
    expedition_type = "登記的難度與遠征隊種類不符。",
    not_leader = "請由遠征隊隊長申請。",
    party_size = "需要 6 至 30 人到場。目前有 " .. result.here .. " 人。",
    roster_absent = "請等全員到齊後再進場。",
    level = "有隊員不符合遠征的等級限制。",
    busy = "已有一隊在洞穴中挑戰。",
}
npc:say(why[result.reason] or "目前無法入場。", false, false)
