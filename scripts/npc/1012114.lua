-- Source: Legacy scripts/npc/1012114.js and TWMS v148 Quest.wz/1200.
-- Source SHA256: 3cadc13e0b2447cb037c4c20b270644700148bf2af25b966db8dc7e181a61d07
if npc:map_id() ~= 910010000 or not npc:event_status("HenesysPQ").inside then
    return
end

local choice = npc:menu("我是興兒。月妙製作的元宵特別好吃，你們能帶 10 個給我嗎？", {
    { id = 0, text = "我們帶來了 10 個元宵" },
    { id = 1, text = "要怎麼完成任務？" },
    { id = 2, text = "離開迎月花山丘" },
})
if choice == 1 then
    npc:say("打倒肥肥拿回#t4001453#，再把種子放在六個花台上。花全開了，月妙就會出現。請保護月妙，讓牠做出 10 個元宵，再由隊長拿給我。", false, false)
elseif choice == 2 then
    if npc:ask_yes_no("離開後就無法回到這一場。確定要離開嗎？") then
        npc:event_leave()
    end
else
    local reason = npc:henesys_finish()
    if reason == "not_leader" then
        npc:say("請讓隊長收集 10 個元宵，再由隊長拿給我。", false, false)
    elseif reason == "cakes" then
        npc:say("請先收集 10 個 #t4001101# 再回來。", false, false)
    elseif reason ~= "ok" then
        npc:say("暫時無法結算。請確認隊員還在場內，稍後再試。", false, false)
    end
end
