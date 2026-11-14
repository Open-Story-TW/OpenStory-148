-- Shared driver for generated Legacy portal scripts. It is appended to a
-- source that defines the global function enter(pi) (and its helpers). The
-- driver supplies `pi` over the portal host, runs enter once, and then:
--   * commits the item, mesos, experience, quest-status and saved-location
--     effects enter collected as ONE atomic exchange (before any warp);
--   * performs the last warp enter asked for (Legacy warped immediately, so
--     a later warp replaced an earlier one; the host revalidates the source
--     portal for every command, so the warp has to come last here);
--   * opens the NPC conversation enter asked for once the portal has ended.
local effects, travel, npc_after, failed

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
    if any and not portal:exchange(spec).ok then failed = true end
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

-- Legacy SavedLocationType. A saved location is kept as the character's
-- script record SAVED_BASE + index (server-side only, never sent to the
-- client); an unknown name fails like SavedLocationType.valueOf did.
local SAVED = {
    FREE_MARKET = 0, MULUNG_TC = 1, WORLDTOUR = 2, FLORINA = 3, FISHING = 4, RICHIE = 5,
    DONGDONGCHIANG = 6, EVENT = 7, AMORIA = 8, CHRISTMAS = 9, ARDENTMILL = 10, Gachapon = 11,
}
local SAVED_BASE = 1900000000
local function saved_id(name)
    local index = SAVED[name]
    if index == nil then error("unknown saved location " .. tostring(name)) end
    return SAVED_BASE + index
end

-- Tutorial hints, effects, sounds and UI locks. They change nothing on the
-- character; the generated sources keep the calls and they show nothing.
local function presentation() end

local function message(kind, text)
    if text == nil then kind, text = 5, kind end
    portal:message(tostring(text), math.tointeger(kind) or 5)
end

local function warp(map, target)
    travel = { map, target or 0 }
end

local function quest_state(id)
    flush()
    return portal:quest_state(id)
end

local function portal_handle(map, target)
    return { __map = map, __portal = target, getId = function() return target end }
end

local function map_handle(id)
    return {
        __map = id,
        getId = function() return id end,
        getPortal = function(target) return portal_handle(id, target) end,
    }
end

local function save_location(name)
    effects.data[#effects.data + 1] = { id = saved_id(name), data = tostring(portal:map_id()) }
end

local level = function() return portal:level() end

local player = {
    dropMessage = message,
    message = message,
    getMapId = function() return portal:map_id() end,
    getJob = function() return portal:job() end,
    getLevel = level,
    getGender = function() return portal:gender() end,
    getMeso = function() return portal:mesos() end,
    changeMap = function(target, entry)
        if type(target) == "table" then
            local map = target.__map
            if type(entry) == "table" then entry = entry.__portal end
            warp(map, entry)
        else
            warp(target, entry)
        end
    end,
}

local here = {
    getName = function() return portal:portal_name() end,
    getId = function() return portal:portal_id() end,
}

local function enter_town(map, minimum, location, denied)
    if portal:map_id() == map then return end
    if level() >= minimum then
        save_location(location)
        warp(map, "st00")
    else
        message(5, denied)
    end
end

pi = {
    warp = warp,
    warpS = warp,
    playPortalSE = presentation,
    playerMessage = message,
    getMapId = function() return portal:map_id() end,
    getPlayer = function() return player end,
    getPortal = function() return here end,
    getMap = map_handle,
    getQuestStatus = quest_state,
    isQuestActive = function(id) return quest_state(id) == 1 end,
    isQuestFinished = function(id) return quest_state(id) == 2 end,
    isQuestCompleted = function(id) return quest_state(id) == 2 end,
    haveItem = function(id, quantity)
        flush()
        return portal:item_count(id) >= (quantity or 1)
    end,
    itemQuantity = function(id)
        flush()
        return portal:item_count(id)
    end,
    canHold = function(id, quantity)
        flush()
        return portal:can_hold(id, quantity or 1)
    end,
    gainItem = function(id, quantity)
        effects.items[#effects.items + 1] = { id = id, quantity = quantity }
    end,
    removeAll = function(id)
        flush()
        local count = portal:item_count(id)
        if count > 0 then effects.items[#effects.items + 1] = { id = id, quantity = -count } end
    end,
    gainExp = function(amount) effects.exp = effects.exp + amount end,
    gainMeso = function(amount) effects.mesos = effects.mesos + amount end,
    forceStartQuest = function(id, data)
        local entry = { id = id, state = 1 }
        if data ~= nil then entry.data = tostring(data) end
        effects.quests[#effects.quests + 1] = entry
    end,
    forceCompleteQuest = function(id)
        effects.quests[#effects.quests + 1] = { id = id, state = 2 }
    end,
    getPlayerCount = function(map) return portal:player_count(map) end,
    getMonsterCount = function() return portal:monster_count() end,
    openNpc = function(id) npc_after = id end,
    getSavedLocation = function(name)
        flush()
        local value = portal:quest_data(saved_id(name))
        if value == "" then return -1 end
        return math.tointeger(tonumber(value)) or -1
    end,
    saveLocation = save_location,
    clearSavedLocation = function(name)
        effects.data[#effects.data + 1] = { id = saved_id(name), data = "" }
    end,
    inFreeMarket = function() enter_town(910000000, 15, "FREE_MARKET", "您必須是15級才能進入自由市場.") end,
    inArdentmill = function() enter_town(910001000, 10, "ARDENTMILL", "您必須是15級才能進入手工藝小鎮.") end,
    getJob = function() return portal:job() end,
    getLevel = level,
    ShowWZEffect = presentation,
    showWZEffect = presentation,
    showInstruction = presentation,
    AranTutInstructionalBubble = presentation,
    showEffect = presentation,
    EnableUI = presentation,
    DisableUI = presentation,
    lockUI = presentation,
    unlockUI = presentation,
    environmentChange = presentation,
    playSound = presentation,
    showMapEffect = presentation,
    summonMsg = presentation,
}

enter(pi)
flush()
if failed then
    message(5, "無法完成這個動作。請確認道具欄有足夠的空間，並且持有需要的道具後再試一次。")
    return
end
-- The host opens the NPC once this portal invocation has ended, wherever
-- the warp below leaves the character.
if npc_after then portal:open_npc(npc_after) end
if travel then
    if math.type(travel[1]) ~= "integer" or travel[1] < 0 or travel[1] > 999999998 then
        -- Legacy failed here too: the saved location it returns to was never
        -- recorded (the character came in some other way), or the source
        -- names a map id that cannot exist.
        message(5, "找不到要返回的地圖，無法傳送。")
        return
    end
    portal:warp(travel[1], travel[2])
end
