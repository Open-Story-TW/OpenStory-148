-- Shared driver for generated Legacy NPC conversations. It is appended to a
-- source that defines __npc, __init and the global functions start/action.
-- Each call of start or action is one legacy invocation: the driver collects
-- its item, mesos, experience, quest-status and record effects, commits them
-- as ONE atomic exchange when the call returns (or earlier, before the call
-- reads something they change), and only then warps, opens the shop or shows
-- the prompt the call asked for. The player's answer becomes the next
-- (mode, type, selection), with the Legacy NPCTalkType numbers. A warp or a
-- shop ends the conversation, so a prompt the same call asked for is dropped.
--
-- The value helpers come first and are single expressions: the debugger
-- checkpoints every statement with the locals in scope, and Legacy menus are
-- built by loops that call these thousands of times inside one step budget.
function __truthy(v)
    return v ~= nil and v ~= false and v ~= 0 and v ~= ""
end

function __join(a)
    local parts = {}
    for i = 0, (a.n or 0) - 1 do parts[#parts + 1] = __text(a[i]) end
    return table.concat(parts, ",")
end

-- JavaScript string conversion: null, whole floats without ".0", arrays.
function __text(v)
    return v == nil and "null"
        or math.type(v) == "float" and v == math.floor(v) and tostring(math.tointeger(v))
        or type(v) == "table" and __join(v)
        or tostring(v)
end

-- JavaScript +: numbers add, anything else concatenates.
function __add(a, b)
    return math.type(a) ~= nil and math.type(b) ~= nil and a + b or __text(a) .. __text(b)
end

-- JavaScript arrays: elements at 0..n-1 and the length in n.
function __array(n, list)
    local a = { n = n }
    for i = 1, n do a[i - 1] = list[i] end
    return a
end

function __isarray(v)
    return type(v) == "table" and v.n ~= nil
end

function __len(v)
    if type(v) == "string" then return utf8.len(v) or #v end
    return v.n
end

function __push(a, v)
    a[a.n] = v
    a.n = a.n + 1
    return a.n
end

function __set(a, i, v)
    a[i] = v
    if a.n ~= nil and math.type(i) == "integer" and i >= a.n then a.n = i + 1 end
end

-- parseInt: the leading integer of a string, or nil where Legacy got NaN.
function __int(v)
    if math.type(v) == "integer" then return v end
    if math.type(v) == "float" then return math.tointeger(v < 0 and math.ceil(v) or math.floor(v)) end
    if type(v) ~= "string" then return nil end
    local digits = string.match(v, "^%s*([+-]?%d+)")
    if digits == nil then return nil end
    return math.tointeger(tonumber(digits))
end

-- JavaScript indexOf on a string (plain text search) or an array.
function __indexof(v, x)
    if type(v) == "table" then
        for i = 0, (v.n or 0) - 1 do
            if v[i] == x then return i end
        end
        return -1
    end
    v = __text(v)
    local at = string.find(v, __text(x), 1, true)
    return at and (utf8.len(v, 1, at - 1) or at - 1) or -1
end

-- JavaScript substring, counted in characters.
function __substring(v, from, to)
    v = __text(v)
    local n = utf8.len(v)
    if n == nil then return string.sub(v, from + 1, to) end
    from = math.max(0, math.min(__whole(from), n))
    to = to == nil and n or math.max(0, math.min(__whole(to), n))
    if from > to then from, to = to, from end
    return string.sub(v, utf8.offset(v, from + 1), utf8.offset(v, to + 1) - 1)
end

function __same(a, b)
    return string.lower(__text(a)) == string.lower(__text(b))
end

-- new Date(): the server clock, read once.
function __date()
    local now = npc:time()
    return {
        getFullYear = function() return now.year end,
        getYear = function() return now.year - 1900 end,
        getMonth = function() return now.month - 1 end,
        getDate = function() return now.day end,
        getDay = function() return now.weekday end,
        getHours = function() return now.hour end,
        getMinutes = function() return now.minute end,
        getSeconds = function() return now.second end,
        getTime = function() return now.ms end,
    }
end

-- A Legacy menu is free text with #L<id>#label#l entries. The text before
-- the first entry is the question; an entry runs to its #l, or to the next
-- entry where the source left the #l out. Returns nil without an entry.
function __menu(text)
    local first = string.find(text, "#L%d+#")
    if first == nil then return nil end
    local choices, seen, at = {}, {}, first
    while true do
        local from, to, digits = string.find(text, "#L(%d+)#", at)
        if from == nil then break end
        at = string.find(text, "#L%d+#", to + 1) or (#text + 1)
        local id = math.tointeger(tonumber(digits))
        if not seen[id] and #choices < 64 then
            seen[id] = true
            choices[#choices + 1] = { id = id, text = __label(string.sub(text, to + 1, at - 1)) }
        end
    end
    return string.sub(text, 1, first - 1), choices
end

function __label(label)
    return (string.gsub(string.gsub(label, "#l.*$", ""), "[\r\n]+$", ""))
end

-- Java int conversion of a script number: a fraction is dropped.
function __whole(v)
    return math.tointeger(v) or math.type(v) == "float" and v == v and math.tointeger(v < 0 and math.ceil(v) or math.floor(v)) or error("expected a number, got " .. __text(v))
end

-- The driver's state lives in this one table (and its functions in the
-- globals below) rather than in locals, for the same checkpoint cost reason:
-- pending prompt, disposed, failed, travel, service, answer, and the effects
-- the current legacy call has collected.
__s = { items = {}, exp = 0, mesos = 0, quests = {}, data = {} }

-- Fame, skill points, job, skills and look the current call changes; they
-- commit in the same exchange as its items and mesos.
function __grow()
    __s.growth = __s.growth or {}
    return __s.growth
end

function __teach(id, level, master)
    local g = __grow()
    g.skills = g.skills or {}
    g.skills[#g.skills + 1] = { id = __whole(id), level = __whole(level), master = master ~= nil and __whole(master) or 0 }
end

function __flush()
    local s = __s
    if s.failed then return end
    local spec, any = {}, false
    if #s.items > 0 then spec.items = s.items; any = true end
    if s.exp > 0 then spec.exp = s.exp; any = true end
    if s.mesos ~= 0 then spec.mesos = s.mesos; any = true end
    if #s.quests > 0 then spec.quests = s.quests; any = true end
    if #s.data > 0 then spec.quest_data = s.data; any = true end
    if s.growth ~= nil then
        local g = s.growth
        if g.hair ~= nil or g.face ~= nil or g.skin ~= nil then
            -- The beauty coupon the source took with gainItem is a cash item:
            -- it is used up together with the look instead.
            for i = #s.items, 1, -1 do
                if s.items[i].id // 10000 == 515 and s.items[i].quantity == -1 and g.coupon == nil then
                    g.coupon = s.items[i].id
                    table.remove(s.items, i)
                end
            end
            if #s.items == 0 then spec.items = nil end
        end
        spec.character = g
        any = true
    end
    s.items, s.exp, s.mesos, s.quests, s.data, s.growth = {}, 0, 0, {}, {}, nil
    if any and not npc:exchange(spec).ok then s.failed = true end
end

function __prompt(kind, text, back, next, speaker)
    text = __text(text)
    __s.pending = { kind = string.find(text, "#L", 1, true) and "simple" or kind, text = text, back = back, next = next, speaker = speaker }
end

-- Legacy sendNextS/sendNextPrevS type byte. Bit 1 hides the close button and
-- bit 2 draws the player as the speaker; the other bits (another NPC's face,
-- type 4 and 8) are not shown, so the line stays in this NPC's window.
function __speaker(kind)
    return kind == nil and 3 or __whole(kind) % 4
end

-- Sounds, screen effects and tutorial hints in Legacy. They change nothing
-- on the character; the generated sources keep the calls and they show
-- nothing here.
function __presentation() end

-- Legacy info quests (cm.getInfoQuest / cm.updateInfoQuest) are kept as the
-- character's script record 1700000000 + id.
function __info(id)
    __flush()
    return npc:quest_data(1700000000 + __whole(id))
end

-- Legacy cm.startQuest / cm.completeQuest run the quest the game data
-- describes; a quest only the scripts know (no game data) just changes state.
function __quest(id, state)
    id = __whole(id)
    __flush()
    if id > 65535 or not pcall(state == 1 and npc.quest_start or npc.quest_complete, npc, id) then
        __force(id, state)
        __flush()
    end
end

-- Legacy cm.setAvatar / cm.setRandomAvatar: 1 when the look changed, -1 when
-- the coupon is missing. A skin colour (below 100) goes through the
-- character changes with the coupon.
function __look(ticket, styles)
    __flush()
    if styles.n > 0 and __whole(styles[0]) < 100 then
        local g = __grow()
        g.skin, g.coupon = __whole(styles[math.random(0, styles.n - 1)]), __whole(ticket)
        __flush()
        local ok = not __s.failed
        __s.failed = nil
        return ok and 1 or -1
    end
    local list, kind = {}, nil
    for i = 0, styles.n - 1 do
        local id = __whole(styles[i])
        local k = id >= 30000 and "hair" or id >= 20000 and "face" or nil
        if k == nil or kind ~= nil and k ~= kind then
            __s.failed = true
            return -1
        end
        kind = k
        list[#list + 1] = id
    end
    if kind == nil then return -1 end
    return npc:change_look(__whole(ticket), kind, list).ok and 1 or -1
end

function __styles(first, ...)
    if __isarray(first) then return first end
    return __array(select("#", ...) + 1, { first, ... })
end

__stats = {
    LVL = function() return npc:level() end,
    GENDER = function() return npc:gender() end,
    HAIR = function() return npc:hair() end,
    FACE = function() return npc:face() end,
    SKIN = function() return npc:stat("skin") end,
    RSP = function() return npc:stat("sp") end,
    RAP = function() return npc:stat("ap") end,
    FAME = function() return npc:stat("fame") end,
    STR = function() return npc:stat("str") end,
    DEX = function() return npc:stat("dex") end,
    INT = function() return npc:stat("int") end,
    LUK = function() return npc:stat("luk") end,
    HP = function() return npc:stat("hp") end,
    MP = function() return npc:stat("mp") end,
    MAXHP = function() return npc:stat("max_hp") end,
    MAXMP = function() return npc:stat("max_mp") end,
    -- "GM" and "ADMIN" are 0: GM-only shortcuts are not offered.
    GM = function() return 0 end,
    ADMIN = function() return 0 end,
}

-- Quest ids above 65535 exist only in the Legacy scripts (the client has no
-- such quest), so their state is kept as script record 1800000000 + id.
function __quest_state(id)
    id = __whole(id)
    __flush()
    if id > 65535 then return math.tointeger(tonumber(npc:quest_data(1800000000 + id))) or 0 end
    return npc:quest_state(id)
end

function __force(id, state, data)
    id = __whole(id)
    if id > 65535 then
        __s.data[#__s.data + 1] = { id = 1800000000 + id, data = tostring(state) }
        return
    end
    __s.quests[#__s.quests + 1] = { id = id, state = state, data = data ~= nil and __text(data) or nil }
end

function __item_count(id)
    __flush()
    return npc:item_count(__whole(id))
end

-- One item change of the current call. The host moves at most 30000 of an
-- item at a time; a larger amount fails the call like a full inventory.
function __item(id, quantity)
    id, quantity = __whole(id), __whole(quantity)
    if quantity == 0 then return end
    if quantity < -30000 or quantity > 30000 then
        __s.failed = true
        return
    end
    __s.items[#__s.items + 1] = { id = id, quantity = quantity }
end

function __message(kind, text)
    if text == nil then kind, text = 5, kind end
    text = __text(text)
    if text ~= "" then npc:message(text, math.tointeger(kind) or 5) end
end

-- Legacy SavedLocationType. A saved location is kept as the character's
-- script record 1900000000 + index (server-side only, never sent to the
-- client), the same records the generated portal sources use.
__saved = {
    FREE_MARKET = 0, MULUNG_TC = 1, WORLDTOUR = 2, FLORINA = 3, FISHING = 4, RICHIE = 5,
    DONGDONGCHIANG = 6, EVENT = 7, AMORIA = 8, CHRISTMAS = 9, ARDENTMILL = 10, Gachapon = 11,
}
function __saved_id(name)
    return 1900000000 + (__saved[name] or error("unknown saved location " .. tostring(name)))
end

function __record(id)
    id = __whole(id)
    return {
        getCustomData = function()
            __flush()
            local data = npc:quest_data(id)
            if data == "" then return nil end
            return data
        end,
        setCustomData = function(data)
            __s.data[#__s.data + 1] = { id = id, data = __text(data) }
        end,
    }
end

__player = {
    getLevel = function() return npc:level() end,
    getJob = function() return npc:job() end,
    getMapId = function() return npc:map_id() end,
    getMeso = function() __flush(); return npc:mesos() end,
    getGender = function() return npc:gender() end,
    getName = function() return npc:name() end,
    -- GM-only shortcuts in Legacy conversations are not offered.
    isGM = function() return false end,
    getGMLevel = function() return 0 end,
    dropMessage = __message,
    itemQuantity = __item_count,
    haveItem = function(id, quantity) return __item_count(id) >= (quantity or 1) end,
    getHair = __stats.HAIR,
    getFace = __stats.FACE,
    getSkinColor = __stats.SKIN,
    getFame = __stats.FAME,
    getStr = __stats.STR,
    getDex = __stats.DEX,
    getInt = __stats.INT,
    getLuk = __stats.LUK,
    getHp = __stats.HP,
    getMp = __stats.MP,
    getRemainingSp = __stats.RSP,
    getRemainingAp = __stats.RAP,
    getSubcategory = function() return npc:stat("sub_job") end,
    hasEquipped = function(id) return npc:has_equipped(__whole(id)) end,
    -- A party is only ever compared with null by the sources kept.
    getParty = function() return npc:party().id ~= 0 and {} or nil end,
    changeMap = function(map, portal) __s.travel = { map, portal or 0 } end,
    getJobId = function() return npc:job() end,
    getAllSkillLevels = function() __flush(); return npc:stat("skill_levels") end,
    addFame = function(amount) __grow().fame = (__grow().fame or 0) + __whole(amount) end,
    -- Legacy sends the new fame to the client here; the exchange does.
    updateFame = function() end,
    gainSP = function(amount) __grow().sp = (__grow().sp or 0) + __whole(amount) end,
    resetSP = function(amount) __grow().sp_set = math.max(0, __whole(amount)) end,
    changeJob = function(job) __grow().job = __whole(job) end,
}

cm = {
    sendNext = function(text) __prompt("say", text, false, true) end,
    sendNextPrev = function(text) __prompt("say", text, true, true) end,
    sendPrev = function(text) __prompt("say", text, true, false) end,
    sendOk = function(text) __prompt("say", text, false, false) end,
    sendYesNo = function(text) __prompt("yes_no", text) end,
    sendAcceptDecline = function(text) __prompt("accept", text) end,
    askAcceptDecline = function(text) __prompt("accept", text) end,
    sendNextS = function(text, kind) __prompt("say", text, false, true, __speaker(kind)) end,
    sendNextPrevS = function(text, kind) __prompt("say", text, true, true, __speaker(kind)) end,
    sendOkS = function(text, kind) __prompt("say", text, false, false, __speaker(kind)) end,
    sendPlayerToNpc = function(text) __prompt("say", text, false, true, 3) end,
    PlayerToNpc = function(text) __prompt("say", text, true, true, 3) end,
    sendNextNoESC = function(text) __prompt("say", text, false, true, 1) end,
    sendSimple = function(text) __prompt("say", text, false, true) end,
    sendGetText = function(text) __prompt("text", text) end,
    sendGetNumber = function(text, default, minimum, maximum)
        __prompt("number", text)
        __s.pending.minimum, __s.pending.maximum = minimum, maximum
    end,
    getText = function() return __s.answer end,
    dispose = function() __s.disposed = true end,
    safeDispose = function() __s.disposed = true end,
    gainItem = __item,
    gainMeso = function(amount) __s.mesos = __s.mesos + __whole(amount) end,
    gainExp = function(amount) __s.exp = __s.exp + __whole(amount) end,
    removeAll = function(id) __item(id, -__item_count(id)) end,
    forceStartQuest = function(id, data) __force(id, 1, data) end,
    forceCompleteQuest = function(id) __force(id, 2) end,
    warp = function(map, portal) __s.travel = { map, portal or 0 } end,
    saveReturnLocation = function(name)
        __s.data[#__s.data + 1] = { id = __saved_id(name), data = tostring(npc:map_id()) }
    end,
    saveLocation = function(name)
        __s.data[#__s.data + 1] = { id = __saved_id(name), data = tostring(npc:map_id()) }
    end,
    clearSavedLocation = function(name)
        __s.data[#__s.data + 1] = { id = __saved_id(name), data = "" }
    end,
    getSavedLocation = function(name)
        __flush()
        return math.tointeger(tonumber(npc:quest_data(__saved_id(name)))) or -1
    end,
    haveItem = __player.haveItem,
    itemQuantity = __item_count,
    canHold = function(id, quantity)
        __flush()
        return npc:can_hold(__whole(id), __whole(quantity or 1))
    end,
    getMeso = __player.getMeso,
    getMapId = __player.getMapId,
    getJob = __player.getJob,
    getLevel = __player.getLevel,
    getName = __player.getName,
    getNpc = function() return __npc end,
    getPlayerStat = function(stat) __flush(); return __stats[stat]() end,
    askAvatar = function(text, styles)
        __prompt("avatar", text)
        __s.pending.kind, __s.pending.styles = "avatar", styles
    end,
    sendStyle = function(text, styles)
        __prompt("avatar", text)
        __s.pending.kind, __s.pending.styles = "avatar", styles
    end,
    setAvatar = function(ticket, style) return __look(ticket, __array(1, { style })) end,
    setRandomAvatar = function(ticket, ...) return __look(ticket, __styles(...)) end,
    -- Asked once per map in one legacy call: menus list a count per room.
    getPlayerCount = function(map)
        map = __whole(map)
        local n = __s.counts[map] or npc:player_count(map)
        __s.counts[map] = n
        return n
    end,
    getChannelNumber = function() return npc:stat("channel") end,
    getCurrentTime = function() return npc:time().ms end,
    getParty = __player.getParty,
    isLeader = function() return npc:party().leader end,
    startQuest = function(id) __quest(id, 1) end,
    completeQuest = function(id) __quest(id, 2) end,
    getInfoQuest = __info,
    updateInfoQuest = function(id, data)
        __s.data[#__s.data + 1] = { id = 1700000000 + __whole(id), data = __text(data) }
    end,
    -- The other conversation opens once this one has ended.
    openNpc = function(id)
        __s.open = __whole(id)
        __s.disposed = true
    end,
    mapMessage = function(kind, text)
        if text == nil then text = kind end
        text = __text(text)
        if text ~= "" then npc:map_message(text) end
    end,
    -- Legacy also announces the prize to the whole world; here the item is
    -- given quietly. Returns the item id, or -1 when it does not fit.
    gainGachaponItem = function(id, quantity)
        id, quantity = __whole(id), __whole(quantity)
        __flush()
        if __s.failed or not npc:can_hold(id, quantity) then return -1 end
        __item(id, quantity)
        __flush()
        return __s.failed and -1 or id
    end,
    getFame = __stats.FAME,
    hasEquipped = __player.hasEquipped,
    changeJob = __player.changeJob,
    teachSkill = __teach,
    addFame = __player.addFame,
    -- The source takes its own coupon or fee in the same call.
    setHair = function(id) __grow().hair = __whole(id) end,
    setFace = function(id) __grow().face = __whole(id) end,
    setSkin = function(id) __grow().skin = __whole(id) end,
    -- Cutscene interface locks: nothing is locked, the dialogue still runs.
    lockUI = __presentation,
    lockKey = __presentation,
    EnableUI = __presentation,
    DisableUI = __presentation,
    getQuestStatus = __quest_state,
    isQuestActive = function(id) return __quest_state(id) == 1 end,
    isQuestStarted = function(id) return __quest_state(id) == 1 end,
    isQuestFinished = function(id) return __quest_state(id) == 2 end,
    isQuestCompleted = function(id) return __quest_state(id) == 2 end,
    getQuestRecord = __record,
    getPlayer = function() return __player end,
    getChar = function() return __player end,
    openShop = function(id) __s.service = { "shop", __whole(id) } end,
    sendStorage = function() __s.service = { "storage" } end,
    playerMessage = __message,
    -- Legacy branches on the client region; this server is not the GMS one.
    isGMS = function() return false end,
    playSound = __presentation,
    showEffect = __presentation,
    environmentChange = __presentation,
    showWZEffect = __presentation,
    ShowWZEffect = __presentation,
    showMapEffect = __presentation,
    summonMsg = __presentation,
    playerSummonHint = __presentation,
    playMusic = __presentation,
    changeMusic = __presentation,
}

-- cm.sendGetNumber: the answer is typed into the text box and asked again
-- until it is a whole number inside the range the source gave.
function __number(p)
    local low = math.type(p.minimum) ~= nil and __whole(p.minimum) or 0
    local high = math.type(p.maximum) ~= nil and __whole(p.maximum) or 2147483647
    local text = p.text
    while true do
        local value = __int(npc:ask_text(text))
        if value ~= nil and value >= low and value <= high then return value end
        text = p.text .. "\r\n\r\n#r請輸入 " .. low .. " 到 " .. high .. " 之間的數字。#k"
    end
end

-- Shows the prompt p and returns the Legacy (mode, type, selection) answer.
function __ask(p)
    if p.kind == "simple" then
        local question, choices = __menu(p.text)
        if question ~= nil then return 1, 5, npc:menu(question, choices) end
        return npc:say(p.text, false, not __s.disposed) and 1 or 0, 0, -1
    elseif p.kind == "yes_no" then
        return npc:ask_yes_no(p.text) and 1 or 0, 2, -1
    elseif p.kind == "accept" then
        return npc:ask_accept(p.text) and 1 or 0, 14, -1
    elseif p.kind == "text" then
        __s.answer = npc:ask_text(p.text)
        return 1, 3, -1
    elseif p.kind == "avatar" then
        local list = {}
        for i = 0, p.styles.n - 1 do list[#list + 1] = __whole(p.styles[i]) end
        local pick = npc:ask_avatar(p.text, list)
        for i = 0, p.styles.n - 1 do
            if __whole(p.styles[i]) == pick then return 1, 8, i end
        end
        return 0, 8, -1
    elseif p.kind == "number" then
        return 1, 4, __number(p)
    end
    -- Once the script has ended behind a line, it only offers to close.
    return npc:say(p.text, p.back and not __s.disposed, p.next and not __s.disposed, p.speaker) and 1 or 0, 0, -1
end

-- Runs one legacy call and settles it. Returns false when the conversation
-- is over, or true with the answer left in __s.mode, __s.kind, __s.selection.
function __step(call)
    local s = __s
    s.pending, s.counts = nil, {}
    call(s.mode, s.kind, s.selection)
    __flush()
    if s.failed then
        npc:say("無法完成這個動作。請確認道具欄有足夠的空間，並且持有需要的道具與楓幣後再試一次。", false, false)
        return false
    end
    if s.open then
        npc:open_npc(s.open)
        return false
    end
    if s.service then
        if s.service[1] == "shop" then npc:open_shop(s.service[2]) else npc:open_storage() end
        return false
    end
    if s.travel then
        local map, target = math.tointeger(s.travel[1]), s.travel[2]
        if map == nil or map < 0 or map > 999999998 then
            -- Legacy failed here too: the saved location it returns to was
            -- never recorded, or the source names a map id that cannot exist.
            npc:say("找不到要前往的地圖，無法傳送。", false, false)
            return false
        end
        npc:warp(map, type(target) == "string" and target or math.tointeger(target) or 0)
        return false
    end
    if s.pending == nil then return false end
    s.mode, s.kind, s.selection = __ask(s.pending)
    return not s.disposed
end

__init()
__s.mode, __s.kind, __s.selection = 1, 0, 0
if __step(start or action) and action ~= nil then
    while __step(action) do end
end
