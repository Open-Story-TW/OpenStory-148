-- Source: Legacy scripts/npc/2010009.js
-- Source SHA256: 5e9a9623bbdff5fa856873ea2a9dfcae52d644e92a9fa478b35bf1ed9a4a8032
-- Guild alliance (Lenario). Party, guild-master and same-field checks are
-- enforced again by the durable alliance commands.
local selected = npc:menu("哈囉 我是雷娜里歐！ 很高興為您服務～#k", {
    {id = 0, text = "我想要知道公會聯盟是什麼？"},
    {id = 1, text = "我要怎麼建立公會聯盟呢？"},
    {id = 2, text = "我想要建立公會聯盟"},
    {id = 3, text = "我想要新增更多的公會到聯盟"},
    {id = 4, text = "我想要解散公會聯盟"},
})
if selected == nil then return end
if selected == 0 then
    npc:say("公會聯盟就是讓兩方的公會成員可以聊天做一些有趣的事情。", false, false)
    return
elseif selected == 1 then
    npc:say("為了成立公會聯盟，兩個公會的會長需要組隊，然後這個組隊裡的隊長就會選為公會聯盟的會長。", false, false)
    return
end
local leader = npc:guild_rank() == 1 and npc:alliance_rank() == 1
if selected == 2 then
    if npc:guild_rank() ~= 1 then
        npc:say("你不能創建一個公會聯盟，直到你有自己的公會。", false, false)
        return
    end
    if npc:alliance_rank() ~= 0 then
        npc:say("你不能再創建因為你已經和其他結為同盟了。", false, false)
        return
    end
    if not npc:ask_yes_no("哦，你有興趣創建一個公會聯盟？") then return end
    while true do
        local name = npc:ask_text("現在請輸入你想要的公會聯盟名稱 (最大字元限制. 12 個字)")
        if name == nil or name == "" then return end
        if npc:ask_yes_no("這個 #b" .. name .. "#k 是你想要的公會聯盟名字嗎？？") then
            if npc:alliance_create(name) then
                npc:say("成功的創建了公會聯盟！！", false, false)
                return
            end
            npc:say("這個名字不能使用，或隊伍裡沒有另一位同地圖的公會長。", true, true)
        end
    end
elseif selected == 3 then
    if not leader then
        npc:say("只有公會聯盟長可以擴大聯盟。", false, false)
    elseif npc:ask_yes_no("為了增加礦大 需要支付 10,000,000 楓幣. 你確定要繼續嗎？") then
        if npc:alliance_capacity() then
            npc:say("你成功增加了公會聯盟容量。", false, false)
        else
            npc:say("很抱歉，由於你的公會聯盟容量已經滿了，所以不能再擴充。", false, false)
        end
    end
else
    if not leader then
        npc:say("只有公會聯盟長才可以解散。", false, false)
    elseif npc:ask_yes_no("你真的想要解散公會聯盟？？") then
        if npc:alliance_disband() then
            npc:say("成功解散公會聯盟。", false, false)
        else
            npc:say("解散公會聯盟時候發生錯誤。", false, false)
        end
    end
end
