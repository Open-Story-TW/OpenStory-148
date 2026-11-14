-- Source: Legacy scripts/npc/22000.js (Shanks).
-- Source SHA256: 36ef1f5ed150eeee95fcfa44dee4c6768e4a3c57f43d6f76eacb1f34d6918369
-- Fare/recommendation and the saved boat map commit as one command.
local cancelled = "恩... 我猜你還有想在這做的事？"
if not npc:ask_yes_no("搭上了這艘船，你可以前往更大的大陸冒險。 只要給我 #e150 楓幣#n，我會帶你去 #b維多利亞島#k。 不過，一旦離開了這裡，就不能再回來囉。 你想要去維多利亞島嗎？") then
    npc:say(cancelled, false, false)
    return
end
local recommendation = npc:item_count(4031801) > 0
if recommendation then
    local text = "既然你有推薦信，我不會收你任何的費用。收起來，我們將前往維多利亞島，坐好，旅途中可能會有點動盪！"
    if not npc:say(text, true, true) then
        npc:say(cancelled, false, false)
        return
    end
    if not npc:say(text, true, true) then
        npc:say(cancelled, false, false)
        return
    end
else
    npc:say("真的只要 #e150 楓幣#n 就能搭船!!")
    if npc:level() < 5 then
        npc:say("讓我看看... 我覺得你還不夠強壯。 你至少要達到7等我才能讓你到維多利亞港囉。", false, false)
        return
    end
    if npc:mesos() < 150 then
        npc:say("什麼？你說你想搭免費的船？ 你真是個怪人！", false, false)
        return
    end
    npc:say("哇! #e150#n 楓幣我收到了！ 好，準備出發去維多利亞港囉！")
end
local result = npc:depart_island()
if not result.ok then
    npc:say("什麼？你說你想搭免費的船？ 你真是個怪人！", false, false)
end
