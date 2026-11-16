local choices = {}
for i = 0, 2 do
    if npc:item_count(4031036 + i) > 0 then
        choices[#choices + 1] = {id = i, text = "#b#m" .. (910360000 + 100 * i) .. "##k"}
    end
end
if npc:item_count(4031711) > 0 then
    choices[#choices + 1] = {id = 3, text = "搭車前往新葉城"}
end
choices[#choices + 1] = {id = 4, text = "捷運線 1 號"}
choices[#choices + 1] = {id = 5, text = "前往墮落廣場"}
local choice = npc:menu("你要去哪裡？", choices)
if choice == nil then return end
if choice < 3 then
    if not npc:subway_challenge(choice) then
        npc:say("請確認你持有該車票。", false, false)
    end
elseif choice == 3 then
    if npc:transit_status() ~= "boarding" then
        npc:say("本班車停止上車，請等下一班。", false, false)
    elseif npc:ask_yes_no("要使用車票搭車前往新葉城嗎？") and not npc:transit_board() then
        npc:say("無法上車，請確認車票或等候下一班。", false, false)
    end
elseif choice == 4 then
    npc:warp(103020100, 0)
elseif choice == 5 then
    npc:warp(103040000, 0)
end
