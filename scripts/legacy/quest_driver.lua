-- Shared driver for generated Legacy quest state machines. It is appended to a
-- source that defines __quest and the global functions start/finish (legacy
-- start/end). Each call of those functions is one legacy invocation: the
-- driver collects its item, experience and quest-status effects, commits them
-- as ONE atomic exchange when the call returns, and only then shows the prompt
-- the call asked for. The player's answer becomes the next (mode, type,
-- selection), with the Legacy NPCTalkType numbers.
local pending, disposed, failed, travel
local effects

local function reset()
    effects = { items = {}, exp = 0, mesos = 0, quests = {}, data = {} }
end
reset()

local function flush()
    if failed then return end
    local spec, any = {}, false
    if #effects.items > 0 then spec.items = effects.items; any = true end
    if effects.exp > 0 then spec.exp = effects.exp; any = true end
    if effects.mesos ~= 0 then spec.mesos = effects.mesos; any = true end
    if #effects.quests > 0 then spec.quests = effects.quests; any = true end
    if #effects.data > 0 then spec.quest_data = effects.data; any = true end
    reset()
    if any and not npc:exchange(spec).ok then failed = true end
end

function __truthy(v)
    return v ~= nil and v ~= false and v ~= 0 and v ~= ""
end

function __add(a, b)
    if math.type(a) ~= nil and math.type(b) ~= nil then return a + b end
    local function text(v)
        if v == nil then return "null" end
        if math.type(v) == "float" and v == math.floor(v) then return tostring(math.tointeger(v)) end
        return tostring(v)
    end
    return text(a) .. text(b)
end

local function prompt(kind, text, back, next, speaker)
    if string.find(text, "#L", 1, true) then kind = "simple" end
    pending = { kind = kind, text = text, back = back, next = next, speaker = speaker }
end

-- Legacy sendNextS/sendNextPrevS type byte. Bit 1 hides the close button and
-- bit 2 draws the player as the speaker; the other bits (another NPC's face,
-- type 4 and 8) are not shown, so the line stays in the quest NPC's window.
local function speaker(kind)
    if kind == nil then return 3 end
    return math.tointeger(kind) % 4
end

-- Tutorial hints, arrows, avatar effects and intro letterboxing in Legacy
-- (summonMsg, AranTutInstructionalBubble, evanTutorial, showWZEffect,
-- MovieClipIntroUI). They change nothing on the character; the generated
-- sources keep the calls and they show nothing here.
local function presentation() end

local function force(state, id, data)
    if type(id) == "string" then id, data = __quest, id end
    local entry = { id = id or __quest, state = state }
    if data ~= nil then entry.data = tostring(data) end
    effects.quests[#effects.quests + 1] = entry
end

local function quest_state(id)
    flush()
    return npc:quest_state(id)
end

qm = {
    sendNext = function(text) prompt("say", text, false, true) end,
    sendNextPrev = function(text) prompt("say", text, true, true) end,
    sendPrev = function(text) prompt("say", text, true, false) end,
    sendOk = function(text) prompt("say", text, false, false) end,
    sendYesNo = function(text) prompt("yes_no", text) end,
    sendAcceptDecline = function(text) prompt("accept", text) end,
    askAcceptDecline = function(text) prompt("accept", text) end,
    sendNextS = function(text, kind) prompt("say", text, false, true, speaker(kind)) end,
    sendNextPrevS = function(text, kind) prompt("say", text, true, true, speaker(kind)) end,
    sendOkS = function(text, kind) prompt("say", text, false, false, speaker(kind)) end,
    sendPlayerToNpc = function(text) prompt("say", text, false, true, 3) end,
    PlayerToNpc = function(text) prompt("say", text, true, true, 3) end,
    summonMsg = presentation,
    playerSummonHint = presentation,
    AranTutInstructionalBubble = presentation,
    evanTutorial = presentation,
    showWZEffect = presentation,
    ShowWZEffect = presentation,
    MovieClipIntroUI = presentation,
    sendSimple = function(text)
        if string.find(text, "#L", 1, true) then prompt("simple", text) else prompt("say", text, false, true) end
    end,
    dispose = function() disposed = true end,
    safeDispose = function() disposed = true end,
    forceStartQuest = function(id, data) force(1, id, data) end,
    forceCompleteQuest = function(id) force(2, id) end,
    gainItem = function(id, quantity)
        effects.items[#effects.items + 1] = { id = id, quantity = quantity }
    end,
    gainExp = function(amount) effects.exp = effects.exp + amount end,
    gainMeso = function(amount) effects.mesos = effects.mesos + amount end,
    haveItem = function(id, quantity)
        flush()
        return npc:item_count(id) >= (quantity or 1)
    end,
    canHold = function(id, quantity)
        flush()
        return npc:can_hold(id, quantity or 1)
    end,
    getQuestStatus = quest_state,
    isQuestActive = function(id) return quest_state(id) == 1 end,
    isQuestFinished = function(id) return quest_state(id) == 2 end,
    isQuestCompleted = function(id) return quest_state(id) == 2 end,
    removeAll = function(id)
        flush()
        local count = npc:item_count(id)
        if count > 0 then effects.items[#effects.items + 1] = { id = id, quantity = -count } end
    end,
    getJob = function() return npc:job() end,
    getPlayer = function()
        return {
            getJob = function() return npc:job() end,
            getLevel = function() return npc:level() end,
        }
    end,
    getQuestCustomData = function()
        flush()
        local data = npc:quest_data(__quest)
        if data == "" then return nil end
        return data
    end,
    setQuestCustomData = function(data)
        effects.data[#effects.data + 1] = { id = __quest, data = tostring(data) }
    end,
    warp = function(map, portal) travel = { map, portal or 0 } end,
}

-- Legacy runs end() for a started quest and start() otherwise; the server has
-- already checked the matching WZ requirements before launching this source.
local phase = start
if npc:quest_state(__quest) == 1 then phase = finish end
if phase == nil then return end

local mode, kind, selection = 1, 0, 0
while true do
    pending = nil
    phase(mode, kind, selection)
    flush()
    if failed then
        npc:say("無法完成這個動作。請確認道具欄有足夠的空間，並且持有需要的道具後再試一次。", false, false)
        return
    end
    if travel then
        npc:warp(travel[1], travel[2])
        return
    end
    local p = pending
    if p == nil then return end
    selection = -1
    if p.kind == "simple" then
        local first = string.find(p.text, "#L", 1, true)
        local choices = {}
        for id, label in string.gmatch(string.sub(p.text, first), "#L(%d+)#(.-)#l") do
            choices[#choices + 1] = { id = math.tointeger(tonumber(id)), text = label }
        end
        selection = npc:menu(string.sub(p.text, 1, first - 1), choices)
        mode, kind = 1, 5
    elseif p.kind == "yes_no" then
        mode, kind = npc:ask_yes_no(p.text) and 1 or 0, 2
    elseif p.kind == "accept" then
        mode, kind = npc:ask_accept(p.text) and 1 or 0, 14
    else
        mode, kind = npc:say(p.text, p.back, p.next, p.speaker) and 1 or 0, 0
    end
    if disposed then return end
end
