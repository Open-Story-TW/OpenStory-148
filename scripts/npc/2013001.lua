local map = npc:map_id()

if map == 920011200 then
    npc:warp(200080101, 0)
    return
end

local status = npc:orbis_stage()
if status == "moved" then
    return
end

local messages = {
    clouds = "請收集 20 個雲朵碎片，放在封印前。",
    not_leader = "請讓隊長和我交談。",
    statue = "散步路、倉庫、休息室、向上通道各有一件碎片。請帶著碎片點擊女神像旁對應的機關。",
    restore = "帶著生命之根點擊女神像，就能救出女神。",
    minerva = "女神已經醒來了。請和她交談。",
    items = "請把這一關找到的道具都交給隊長，再和我交談。",
    music = "找出和今天星期相同的唱片，再將它放到唱機上。",
    levers = "頂端三個把手中，有兩個應該開啟。請再確認。",
    garden = "帶著種子點擊花盆，找出並擊敗遠古精靈。",
    already = "這一關的碎片已經拿到了。",
    claimed = "道具已交給隊長。",
}
npc:say(messages[status] or "目前沒有可以領取的道具。", false, false)
