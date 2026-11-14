-- Reviewed conversion of Legacy scripts/quest/20013.js (裝有道具的箱子).
-- Original SHA256: 4b60cd525f93db516555631763c87a4b24432e900bdb2f9ae749b4b629e3df1e.
-- Legacy stepped back one line on an early decline and looped until ESC; here
-- a decline ends the talk. summonMsg tutorial hints show nothing here.
local state = npc:quest_state(20013)
if state == 0 then
    if npc:quest_state(20012) ~= 2 then return end
    npc:say("#b(*噹噹*)#k")
    npc:say("嘿！你嚇到我了！我不知道我有一個訪客。你就是 #p1102006# 說的那位貴族吧。歡迎！我是 #p1102007#，我的興趣是製作#b椅子#k。我正在考慮做一張椅子當作歡迎你的禮物。", true, true)
    npc:say("別急，我還不能給你椅子，因為我沒有足夠的材料。你能幫我找材料嗎？在這附近有很多箱子，裡面有各種物品。請從那些箱子裡幫我帶回一個 #t4032267# 和一個 #t4032268#。", true, true)
    npc:say("你知道怎麼打破箱子嗎？使用你的#b普通攻擊#k就可以敲破箱子。", true, true)
    if not npc:ask_accept("請從那些箱子裡幫我拿 1個 #b#t4032267##k 和 1個 #b#t4032268##k。然後我就會做一張最棒的椅子給你，我會在這裡等你！") then
        npc:say("如果需要就回來找我。", false, false)
        return
    end
    npc:exchange({ quests = { { id = 20013, state = 1 } } })
    return
end
if state ~= 1 then return end
if npc:item_count(4032267) < 1 or npc:item_count(4032268) < 1 then
    npc:say("請從附近的箱子裡幫我拿 1個 #b#t4032267##k 和 1個 #b#t4032268##k。", false, false)
    return
end
npc:say("你已經完成了？\r\n太棒了！")
npc:say("來，這是給你的 #t3010060#。你覺得怎麼樣？很漂亮吧呵呵^^ 你可以#b把椅子放到快捷鍵上使用，讓HP恢復得更快#k。椅子在道具欄的#b裝飾欄#k裡，請確認是不是收到椅子了。好了，請照著箭頭的指示走，你會看到另一個人。 \r\n\r\n#fUI/UIWindow.img/QuestIcon/4/0# \r\n#i3010060# 1 #t3010060# \r\n#fUI/UIWindow.img/QuestIcon/8/0# 95 經驗值", true, true)
-- Legacy also finishes the older Neinheart/Kiku path (20000-20002, 20015) so
-- the job path 20100 opens from either tutorial.
local result = npc:exchange({
    exp = 95,
    items = { { id = 4032267, quantity = -1 }, { id = 4032268, quantity = -1 }, { id = 3010060, quantity = 1 } },
    quests = { { id = 20013, state = 2 }, { id = 20000, state = 2 }, { id = 20001, state = 2 }, { id = 20002, state = 2 }, { id = 20015, state = 2 } },
})
if not result.ok then
    npc:say("請確認裝飾欄有空位後再來找我。", false, false)
end
