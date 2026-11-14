-- Source: Legacy scripts/npc/1002007.js (Lith Harbor Regular Cab).
-- Source SHA256: 01e30d95f6912da9ae7a32bd17ed4f1a35f74c9105b28694b7c9b10e1f8d4234
-- Legacy has no start(): NPCScriptManager invokes action(1,0,0), entering status1.
-- Fare/coupon and saved destination commit together through the dedicated host.
local maps = {120000000, 102000000, 100000000, 103000000}
local fares = {1200, 1000, 1000, 1200}
local labels = {"1,200", "1,000", "1,000", "1,200"}
local introduction = "Hi! I drive the Lith Harbor Regular Cab. Would you like to travel to a different town? If so, try using my cab. I can take you to a different town for a cheap price."
local cancellation = "There's a lot to see in this town, too. Come back and find us when you need to go to a different town."
local insufficient = "You don't have enough mesos. Sorry to say this, but without them, you won't be able to ride the cab."
local show_introduction = false
while true do
    if show_introduction then
        npc:say(introduction, false, true)
    end
    if npc:item_count(4032313) > 0 then
        if npc:say("Hey, since you have a Taxi Coupon, I can take you to the town indicated on the pass for free. It looks like your destination is #bHenesys#k!", true, true) then
            local result = npc:take_taxi(2, 0)
            if not result.ok then
                npc:say(cancellation, false, false)
            end
            return
        end
        show_introduction = true
    else
        local job = npc:job()
        local beginner = job == 0 or job == 1000 or job == 2000
        local text = "Choose your destination, for fees will change from place to place.#b"
        if beginner then
            text = "We have a special 90% discount for beginners. Choose your destination, for fees will change from place to place.#b"
        end
        local choices = {}
        for i = 1, #maps do
            local price = labels[i]
            if beginner then
                price = tostring(fares[i] // 10)
            end
            choices[i] = {id = i - 1, text = "#m" .. maps[i] .. "# (" .. price .. " mesos)"}
        end
        local selected = npc:menu(text, choices)
        local fare = fares[selected + 1]
        local shown = labels[selected + 1]
        if beginner then
            fare = fare // 10
            shown = tostring(fare)
        end
        if not npc:ask_yes_no("You don't have anything else to do here, huh? Do you really want to go to #b#m" .. maps[selected + 1] .. "##k? It'll cost you #b" .. shown .. " mesos#k.") then
            npc:say(cancellation, false, false)
            return
        end
        if npc:mesos() < fare then
            npc:say(insufficient, false, false)
            return
        end
        local result = npc:take_taxi(selected, fare)
        if not result.ok then
            npc:say(insufficient, false, false)
        end
        return
    end
end
