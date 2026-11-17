-- Legacy 2094002 and TWMS v148 Quest.wz/1204.
if npc:map_id() == 925100700 then
    local items = {}
    for _, id in ipairs({4001117, 4001120, 4001121, 4001122}) do
        local count = npc:item_count(id)
        if count > 0 then items[#items + 1] = {id = id, quantity = -count} end
    end
    if #items == 0 or npc:exchange({items = items}).ok then npc:warp(251010404, 0) end
    return
end
local result = npc:pirate_pq_stage()
local text = {
    not_leader = "請讓你們的隊長來跟我說話。",
    medals_done = "三種海賊勳章都收齊了。請前往下一關。",
    doors = "把 #t4001117# 丟在四扇門上，才能封住海賊船的力量。",
    captain = "請一起擊敗金勾海賊王，然後由隊長來找我。",
    guards = "擊敗海賊船上的守衛，再從傳送點前進。",
}
if result.state == "medals" then
    npc:say("請交給我 20 個 #b#t" .. result.item .. "##k。", false, false)
elseif text[result.state] then
    npc:say(text[result.state], false, false)
end
