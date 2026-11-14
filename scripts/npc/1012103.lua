-- Source: Legacy scripts/npc/1012103.js (娜塔麗, 弓箭手村美髮店院長)
-- Source SHA256: 6fc8246b08f2b2b6f1c4cdc3b204be265d1c6aad2f1b47eb610ca1bd8d030fec
-- VIP haircut (#t5150001#) and VIP dye (#t5151001#): the player previews and
-- picks a style; the coupon and the new look commit together.
local HAIR_COUPON, DYE_COUPON = 5150001, 5151001
local choice = npc:menu("我是這家美髮店的院長。如果你有#b#t5150001##k或#b#t5151001##k，就讓我來幫你打理髮型吧。請選擇你想要的服務。", {
    { id = 0, text = "剪髮：#i5150001##t5150001#" },
    { id = 1, text = "染髮：#i5151001##t5151001#" },
})
local hair = npc:hair()
if choice == 0 then
    local styles
    if npc:gender() == 0 then
        styles = { 30030, 30020, 30000, 30310, 30330, 30060, 30150, 30410, 30210, 30140, 30120, 30200 }
    else
        styles = { 31050, 31040, 31000, 31150, 31310, 31300, 31160, 31100, 31410, 31030, 31080, 31070 }
    end
    for i = 1, #styles do
        styles[i] = styles[i] + hair % 10
    end
    local pick = npc:ask_avatar("我可以讓你的髮型煥然一新，要不要換個造型看看？只要有#b#t5150001##k，我就幫你換。挑一個你喜歡的吧～", styles)
    local result = npc:change_look(HAIR_COUPON, "hair", pick)
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
    local pick = npc:ask_avatar("我可以幫你換個漂亮的髮色，要不要試試看？只要有#b#t5151001##k，我就幫你染。挑一個你喜歡的顏色吧。", colors)
    local result = npc:change_look(DYE_COUPON, "hair", pick)
    if result.ok then
        npc:say("好好享受你的新髮色吧！", false, false)
    else
        npc:say("嗯…你好像沒有我們指定的染髮券…沒有它我沒辦法幫你染頭髮，真抱歉…", false, false)
    end
end
