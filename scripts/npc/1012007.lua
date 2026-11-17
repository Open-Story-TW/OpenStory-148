-- Pet-Walking Road turn-in from the TMS trainer script and the v148 WZ map.
if npc:map_id() ~= 100000202 then return end
if npc:item_count(4031035) == 0 then
    npc:say("請先向我的哥哥拿信，再帶著你的寵物來找我。", false, false)
    return
end
if not npc:ask_yes_no("你帶著哥哥的信走完了障礙路。要讓我幫你提高寵物的親密度嗎？") then return end
if npc:pet_training() then
    npc:say("你召喚中的寵物各增加了 2 點親密度。下次還可以再來訓練。", false, false)
else
    npc:say("請先召喚你要訓練的寵物，並確認還帶著哥哥的信。", false, false)
end
