-- Source: Legacy scripts/npc/1012104.js (伯麗特, 弓箭手村美髮店助理)
-- Source SHA256: 571fc2df35ce0e65cb25c2c026dc3040aa38f237e71bfa075f46f0cde6cfdfdf
-- Random haircut and random dye. Legacy advertised #t5150010# but took
-- #t5150000#; this accepts the regular coupon first, then the EXP coupon.
local choice = npc:menu("我是助理伯麗特。如果你剛好有#b#t5150000##k、#b#t5150010##k或#b#t5151000##k，要不要讓我幫你換個髮型？", {
    { id = 0, text = "剪髮：#i5150000##t5150000#" },
    { id = 1, text = "染髮：#i5151000##t5151000#" },
})
local hair = npc:hair()
if choice == 0 then
    local styles
    if npc:gender() == 0 then
        styles = { 30310, 30330, 30060, 30150, 30410, 30210, 30140, 30120, 30200, 30560, 30510, 30610, 30470 }
    else
        styles = { 31150, 31310, 31300, 31160, 31100, 31410, 31030, 31080, 31070, 31610, 31350, 31510, 31740 }
    end
    for i = 1, #styles do
        styles[i] = styles[i] + hair % 10
    end
    if not npc:ask_yes_no("使用美髮券的話，髮型會#r隨機#k改變，說不定會出現連你都想不到的新造型喔。確定要使用#b#t5150000##k（或#b#t5150010##k）來換髮型嗎？") then
        return
    end
    local coupon = 5150000
    if npc:item_count(coupon) == 0 then
        coupon = 5150010
    end
    local result = npc:change_look(coupon, "hair", styles)
    if result.ok then
        npc:say("好好享受你的新髮型吧！", false, false)
    else
        npc:say("嗯…你好像沒有我們指定的美髮券…沒有它我沒辦法幫你剪頭髮，真抱歉…", false, false)
    end
else
    local base = hair - hair % 10
    local colors = {}
    for i = 0, 7 do
        colors[#colors + 1] = base + i
    end
    if not npc:ask_yes_no("使用一般染髮券的話，髮色會#r隨機#k改變。確定要使用#b#t5151000##k來換髮色嗎？") then
        return
    end
    local result = npc:change_look(5151000, "hair", colors)
    if result.ok then
        npc:say("好好享受你的新髮色吧！", false, false)
    else
        npc:say("嗯…你好像沒有我們指定的染髮券…沒有它我沒辦法幫你染頭髮，真抱歉…", false, false)
    end
end
