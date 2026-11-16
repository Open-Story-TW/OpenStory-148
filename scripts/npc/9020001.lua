-- Adapted from Legacy scripts/npc/9020001.js under the script license.
local event = npc:event_status("KerningPQ")
local map = npc:map_id()
local stage = (map - 910340000) // 100
if not event.inside or stage < 1 or stage > 5 or map % 100 ~= 0 then
    return
end

local COUPON, PASS, LIQUID = 4001007, 4001008, 4001531
local leader = npc:party().leader
local cleared = tostring(stage) .. "stageclear"
local function say(text)
    npc:say(text, false, false)
end
local function log_key(action)
    return "kerning_" .. event.attempt .. "_" .. action
end
local function completed(action)
    return npc:log("character", "permanent", log_key(action)) == 1
end
local function exchange_once(action, items)
    return npc:exchange({
        items = items,
        logs = {{scope = "character", period = "permanent", name = log_key(action), delta = 1, maximum = 1}},
    }).ok
end
local function clear(exp)
    if exp then
        npc:event_party_exp(exp)
    end
    npc:event_set(cleared, "true")
    npc:event_effect("clear")
end

if npc:event_get(cleared) == "true" then
    if stage < 5 then
        say("這一關已經通過了，請前往下一關。")
        return
    end
    if not completed("reward") then
        if not npc:ask_yes_no("恭喜完成第一次同行！要領取獎勵並前往獎勵地圖嗎？") then
            return
        end
        local prizes = {
            {{2040502, 1}, {2040505, 1}, {2040802, 1}, {2040002, 1}, {2040402, 1}, {2040602, 1}},
            {{1032004, 1}, {1032005, 1}, {1032009, 1}, {1032006, 1}, {1032007, 1}, {1032010, 1},
             {1032002, 1}, {1002026, 1}, {1002089, 1}, {1002090, 1}},
            {{2000001, 80}, {2000002, 80}, {2000003, 80}, {2000006, 50}, {2000004, 5}, {2022000, 15}, {2022003, 15}},
            {{4010000, 15}, {4010001, 15}, {4010002, 15}, {4010003, 15}, {4010004, 8}, {4010005, 8}, {4010006, 8},
             {4020000, 8}, {4020001, 8}, {4020002, 8}, {4020003, 8}, {4020004, 8}, {4020005, 8}, {4020006, 8},
             {4020007, 3}, {4020008, 3}, {4003000, 30}},
        }
        local set = prizes[math.min(4, (math.random(10) + 2) // 3)]
        local prize = set[math.random(#set)]
        local items = {{id = prize[1], quantity = prize[2]}, {id = LIQUID, quantity = 1}}
        for _, id in ipairs({COUPON, PASS}) do
            local quantity = npc:item_count(id)
            if quantity > 0 then
                items[#items + 1] = {id = id, quantity = -quantity}
            end
        end
        if not exchange_once("reward", items) then
            say("目前無法領取獎勵，請先確認背包有空位。")
            return
        end
    end
    npc:warp(910340600, 0)
    return
end

if stage == 1 then
    if leader then
        local need = event.members - 1
        if not completed("stage1") and (npc:item_count(PASS) < need or
                not exchange_once("stage1", {{id = PASS, quantity = -need}})) then
            say("請隊員各自回答問題，再把通行證交給隊長。你需要交給我 #b" .. need .. "#k 張通行證。")
            return
        end
        clear(100)
        say("通行證數量正確，請前往下一關。")
        return
    end
    if completed("answer") then
        say("請把剛取得的通行證交給隊長。")
        return
    end
    local questions = {
        {"劍士第一次轉職需要的最低等級", 10},
        {"劍士第一次轉職需要的最低力量", 35},
        {"法師第一次轉職需要的最低智力", 20},
        {"弓箭手第一次轉職需要的最低敏捷", 25},
        {"盜賊第一次轉職需要的最低敏捷", 25},
        {"第二次轉職需要的最低等級", 30},
    }
    local key = "question_" .. npc:character_id()
    local index = tonumber(npc:event_get(key))
    if not index then
        index = math.random(#questions)
        npc:event_set(key, tostring(index))
    end
    local question = questions[index]
    if npc:item_count(COUPON) ~= question[2] then
        say("請打倒鱷魚，收集與答案相同張數的 #b#t4001007##k。\r\n題目：" .. question[1] .. "是多少？")
        return
    end
    if exchange_once("answer", {{id = COUPON, quantity = -question[2]}, {id = PASS, quantity = 1}}) then
        say("答案正確！請把通行證交給隊長。")
    else
        say("目前無法交換通行證，請確認背包後再試一次。")
    end
    return
end

if not leader then
    say("請隊長來確認這一關是否完成。")
    return
end

if stage == 2 or stage == 3 then
    local key = "combination_" .. stage
    local answer = tonumber(npc:event_get(key))
    if not answer then
        local count = stage + 2
        local first = math.random(count)
        local second = math.random(count - 1)
        if second >= first then
            second = second + 1
        end
        npc:event_set(key, tostring((1 << (first - 1)) | (1 << (second - 1))))
        say("請讓隊員站在正確的兩個位置，或把道具放在上面，再由隊長來確認答案。")
        return
    end
    if npc:event_areas() ~= answer then
        npc:event_effect("wrong")
        say("位置不對。請更換組合，並確認只佔了兩個位置。")
        return
    end
    clear(stage == 2 and 200 or 400)
elseif stage == 4 then
    if npc:monster_count() > 0 then
        say("打倒地圖裡的所有怪物後，再來找我。")
        return
    end
    clear(700)
else
    if npc:monster_count() > 0 or (not completed("stage5") and
            (npc:item_count(PASS) < 1 or not exchange_once("stage5", {{id = PASS, quantity = -1}}))) then
        say("請打倒首領，並帶一張通行證回來。")
        return
    end
    clear()
end
say("通過了！" .. (stage == 5 and "請再和我對話領取獎勵。" or "請前往下一關。"))
