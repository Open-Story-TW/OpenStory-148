-- Source: Legacy scripts/npc/9010022.js (次元之鏡, 萬能傳送點)
-- Source SHA256: c76be9e0089ba6d5e7e319952c2e9cdfae8815fc354cd4ce4ccce336f8f31241
-- The client's slide menu (cm.askMapSelection) lists the destinations open to
-- the character's level and answers with the chosen id. Differences from
-- Legacy: under level 10 the refusal is a dialogue page instead of a chat
-- line; 中式結婚 (700000000) is left out because the server's map data does
-- not have that map. Escape uses the saved MULUNG_TC return location read
-- by prisonBreak_exit.lua.
local level = npc:level()
if level < 10 then
    npc:say("沒有可以通過次元之鏡移動的地方。", false, false)
    return
end

-- id = Legacy selection number, map/portal = Legacy cm.warp arguments.
local entries = {
    { id = 801, text = "海外旅遊", map = 950000000, portal = 0, min = 10 },
    { id = 9, text = "月妙的年糕", map = 910010500, portal = 0, min = 50 },
    { id = 10, text = "組隊訓練場", map = 910340700, portal = 0, min = 25, max = 30 },
    { id = 2, text = "怪物嘉年華1", map = 980000000, portal = 4, min = 30, max = 50 },
    { id = 3, text = "怪物嘉年華2", map = 980030000, portal = 4, min = 51, max = 70 },
    { id = 5, text = "奈特的金字塔", map = 926010000, portal = 4, min = 60, max = 109 },
    { id = 11, text = "時空的裂縫", map = 221023300, portal = 0, min = 20 },
    { id = 12, text = "毒霧森林", map = 300030100, portal = 0, min = 70 },
    { id = 13, text = "女神的痕跡", map = 200080101, portal = 0, min = 70 },
    { id = 14, text = "老海盜", map = 251010404, portal = 0, min = 70 },
    { id = 15, text = "羅密歐與茱麗葉", map = 261000021, portal = 0, min = 70 },
    { id = 16, text = "侏儒怪皇帝的復活", map = 211000002, portal = 0, min = 120 },
    { id = 17, text = "御龍魔", map = 240080000, portal = 0, min = 120 },
    { id = 21, text = "陷入危險的坎特", map = 923040000, portal = 0, min = 120 },
    { id = 22, text = "逃脫", map = 921160000, portal = 0, min = 120 },
}

local offered = {}
local byID = {}
for _, entry in ipairs(entries) do
    if level >= entry.min and (entry.max == nil or level <= entry.max) then
        offered[#offered + 1] = { id = entry.id, text = entry.text }
        byID[entry.id] = entry
    end
end

local choice = npc:ask_map_selection(offered)
local destination = byID[choice]
if destination.map == 921160000 then
    local saved = npc:exchange({quest_data = {{id = 1900000001, data = tostring(npc:map_id())}}})
    if not saved.ok then
        npc:say("暫時無法記錄返回位置，請再試一次。", false, false)
        return
    end
end
npc:warp(destination.map, destination.portal)
