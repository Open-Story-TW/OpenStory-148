-- Reviewed conversion of Legacy scripts/quest/1021.js (Roger's Apple).
-- Original SHA256: c684641e5bf1af4bace633ddd8f8e3c759ebbd4b77915e9eed14aa943cc1dc92.
-- Recovery uses permanent quest state. Acceptance atomically grants the apple,
-- changes HP and starts the quest before the next dialogue wait.
local state = npc:quest_state(1021)
if state == 0 then
    local page = 1
    local pages = {
        "嗨, 怎麼了嗎? 我是羅傑，可以教你一些有用的知識。",
        "你問我為什麼在這嗎? 哈哈哈!\r我想要教導那些剛進楓之谷的冒險者們。"
    }
    while page <= #pages do
        if npc:say(pages[page], page > 1, true) then page = page + 1
        else page = page - 1 end
    end
    if not npc:ask_yes_no("所以..... 讓我們來玩點有趣的~!") then return end
    npc:quest_start(1021)
    npc:say("哈哈，嚇死你了吧！ 如果你血量歸零將是個大問題的。 那現在我會給你一顆 #r羅傑的蘋果#k。 請把它吃下去，你會變得更有活力的。按下鍵盤上的#bI#k 來打開物品欄")
    npc:say("請先把剛剛給你的蘋果吃下去，你會發現你的血量會上升，等你血量 100% 的時候再來跟我說話")
elseif state == 1 then
    if npc:hp() < 50 then
        npc:say("哈囉，你還沒把我給你的蘋果吃掉啊，趕快吃了再來找我吧。")
        return
    end
    local pages = {
        "你看～是不是很簡單？ 你可以在右側的欄位設定#b熱鍵#k。 哈哈，你聽不懂對吧？ 喔，每隔一段時間，血量就會恢復了。 雖然很花時間，但好好運用的話可以幫助不少的。",
        "Alright! Now that you have learned alot, I will give you a present. This is a must for your travel in Maple World, so thank me! Please use this under emergency cases!",
        "好了，該教你的都教了，該說再見了。 祝你好運囉！\r\n\r\n#fUI/UIWindow.img/QuestIcon/4/0#\r\n#v2010000# 3 #t2010000#\r\n#v2010009# 3 #t2010009#\r\n\r\n#fUI/UIWindow.img/QuestIcon/8/0# 10 exp"
    }
    local page = 1
    while page <= #pages do
        if npc:say(pages[page], page > 1, true) then page = page + 1
        else page = page - 1 end
    end
    npc:quest_complete(1021)
end
