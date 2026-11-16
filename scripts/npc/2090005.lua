local where = npc:map_id()
local choices
if where == 200000141 then
    choices = {{id = 0, text = "前往武陵，6,000 楓幣"}}
elseif where == 250000100 then
    choices = {{id = 1, text = "前往天空之城，6,000 楓幣"}, {id = 2, text = "前往桃花仙境，1,500 楓幣"}}
elseif where == 251000000 then
    choices = {{id = 3, text = "前往武陵，1,500 楓幣"}}
else
    return
end
local route = npc:menu("歡迎搭乘仙鶴。你想去哪裡？", choices)
if route ~= nil and npc:ask_yes_no("確定要支付費用並出發嗎？") and not npc:hak_board(route) then
    npc:say("目前無法搭乘。請確認楓幣足夠，或等仙鶴回來。", false, false)
end
