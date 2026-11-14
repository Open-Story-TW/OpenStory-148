-- Source: Legacy scripts/npc/1090000.js
-- Source SHA256: af9cdae9e1551618e366445d46e88958e7bfd8702831c85d3f68a0ef681f14fd
-- Ordinary beginner-to-Pirate only; Cannoneer and later branches are pending.
if npc:job() ~= 0 then
    npc:say("我目前只能協助冒險家完成第一次轉職。", false, false)
    return
end
local selected = npc:menu("Have you got something to say?", {
    {id = 0, text = "I would like to learn more about pirates..."}
})
if selected == nil then
    return
end
npc:say("Do you wish to become a Pirate? You'll need to meet our set of standards if you are to become one of us. I need you to be #bat least Level 10, with DEX at 20 or above.#k Let's see...")
-- The original requirements code checks level only. DEX20 is the later reset.
if npc:level() < 10 then
    npc:say("Hmm... I don't think you have trained enough, yet. See me when you get stronger.", false, false)
    return
end
if not npc:ask_yes_no("You seem more than qualified! Great, are you ready to become one of us?") then
    npc:say("I see... Well, selecting a new job is a very important decision to make. If you are ready, then let me know!", false, false)
    return
end
npc:say("Welcome to the band of Pirates! You may have to spend some time as a wanderer at first, but better days will certainly dawn upon you, sooner than you think! In the mean time, let me share some of my abilities with you.")
-- The host commits changeJob THEN resetStats(4,20,4,4), the two weapons and
-- three separate 600-bullet stacks. The source contains no bag expansion call.
local result = npc:advance_first_job(500)
if not result.ok then
    npc:say("請確認裝備欄有兩格空間、消耗欄有三格空間，並且已符合轉職條件。", false, false)
    return
end
npc:say("I have just increased the number of slots for your equipment and etc. inventory. You have also gotten a bit stronger. Can you feel it? Now that you can officially call yourself a Pirate, join us in our quest for adventure and freedom!")
npc:say("I have just given you a little bit of #bSP#k. Look at the #bSkill menu#k to find some skills, and use your SP to learn the skills. Beware that not all skills can be enhanced from the get go. There are some skills that you can only acquire after mastering basic skills.")
npc:say("One more thing. Now that you have graduated from the ranks of a Beginner into a Pirate, you'll have to make sure not to die prematurely. If you do lose all your health, you'll lose valuable EXP that you have earned. Wouldn't it stink to lose hard-earned EXP by dying?")
npc:say("This is all I can teach you. I have also given you some useful weapons to work with, so it's up to you now to train with them. The world is yours for the taking, so use your resources wisely, and when you feel like you've reached the top, let me know. I'll have something better for you in store...")
npc:say("Oh, and... if you have more questions about being a Pirate, or if you need some pointers... you can always ask me. I'll see you around...", false, false)
