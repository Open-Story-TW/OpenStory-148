local choice = npc:menu("要接受另一支隊伍的挑戰嗎？", {
    { id = 1, text = "隊長接受挑戰" },
    { id = 0, text = "取消等候，返回大廳" },
})
if choice == 0 then
    npc:carnival_ii_exit()
elseif choice == 1 then
    local result = npc:carnival_ii_accept()
    if result ~= "accepted" then
        npc:say("目前沒有可由你接受的挑戰。", false, false)
    end
end
