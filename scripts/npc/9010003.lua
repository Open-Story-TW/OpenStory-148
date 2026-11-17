if npc:map_id() ~= 101000000 then
    return
end
if npc:ask_yes_no("你要花 1,000,000 楓幣進入審判之地嗎？") then
    local result = npc:judgement_enter()
    if result == "level" then
        npc:say("至少角色等級 21 才能參加。", false, false)
    elseif result == "closed" then
        npc:say("審判之地尚未開放報名。", false, false)
    elseif result == "fare" then
        npc:say("無法入場，請確認你有足夠的楓幣。", false, false)
    end
end
