-- 蘇功：武陵道場（一般模式）的挑戰入口；在各樓層可以帶人離開道場。
local map = npc:map_id()
if map ~= 925020001 then
    if not npc:event_status("MuLungDojo").inside then
        return
    end
    if npc:ask_yes_no("要放棄這次挑戰、離開道場嗎？") then
        npc:event_leave()
    end
    return
end
local selection = npc:menu("你是來向武公挑戰的嗎？", {
    {id = 0, text = "挑戰武陵道場"},
    {id = 1, text = "聽聽道場的規矩"},
})
if selection == 1 then
    npc:say("道場共有 47 層，每層都有時間限制。打倒該層的對手後，從上方的門前往下一層；每 6 層有一間休息室。時間到了就會被送出道場。", false, true)
    npc:say("在道場裡攻擊對手會累積氣力。氣力集滿時，按住 Ctrl + Alt 再按方向鍵 ↑、←、→，就能使出竹竿千擊、金剛不壞和地火天爆。竹竿千擊每一層只能用一次。", true, false)
    return
end
if not npc:ask_yes_no("要開始挑戰嗎？如果你是隊長，在大廳的隊員會一起進入。") then
    return
end
local result = npc:event_start("MuLungDojo")
if result.ok then
    return
end
local answers = {
    busy = "現在所有的道場都有人在挑戰，請稍後再來。",
    not_leader = "組隊時請由隊長來申請。",
    party_size = "隊伍最多 6 人。",
    unavailable = "現在無法進入道場，請稍後再試。",
}
npc:say(answers[result.reason] or "現在無法進入道場，請稍後再試。", false, false)
