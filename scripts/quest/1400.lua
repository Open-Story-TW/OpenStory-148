-- Reviewed conversion of Legacy scripts/quest/1400.js (五種冒險的方法).
-- Original SHA256: 702157197f640cb7547e914c9ce7f034a28077e7fc3038acb31eb9d279cb655f.
-- Recovery uses permanent quest state: a reconnect between acceptance and
-- completion asks for the path again. The chosen path is custom record 1406,
-- written before completion so a finished quest always has a recommendation.
local state = npc:quest_state(1400)
if state == 2 then return end
if not npc:ask_yes_no("嗯，你決定好你想轉職的職業了嗎？你可以成為一個擁有強大力量與體力的劍士或一個擁有魔法的法師或一個使用箭矢的弓箭手以及一個可快速攻擊的盜賊或者使用各種華麗連鎖技的海盜…有這麼多！") then return end
local sel = npc:menu("只要去維多利亞島的話，就可以透過轉職官轉職為想要的職業。在那之前，若是提前告知想要的職業，我會寫推薦信給他們的。若是事先獲得推薦，就可以輕易進行轉職。你打算選哪一項職業呢？", {
    { id = 0, text = "擁有強大力量與體力的劍士" },
    { id = 1, text = "會利用各種魔法與敵人戰鬥的法師" },
    { id = 2, text = "會利用迅速且正確的弓箭在遠處攻擊的弓箭手" },
    { id = 3, text = "在黑暗中發揮能力的盜賊" },
    { id = 4, text = "以各種連續技為傲的海盜" },
})
if state == 0 then npc:quest_start(1400) end
local about = {
    "#b戰士#k是具有強大攻擊力和體力的職業，在戰場的最前線發揮作用。基本攻擊非常強大的職業，不斷學習高級技術，能夠發揮更強大的力量。",
    "#b法師#k擁有華麗效果的屬性魔法和組隊狩獵時非常實用的各種輔助魔法。而且2轉後能夠學習的屬性魔法可以對相反屬性的敵人賦予致命的傷害。",
    "#b弓箭手#k是具有敏捷性和力量的職業，在戰場的後方負責遠距離攻擊，利用地形的狩獵也非常強有力。",
    "#b盜賊#k是具有運氣和一定敏捷性和力量的職業，在戰場上經常使用突襲對方或藏身的特殊技能。具有相當高的機動性和回避率的盜賊，具有各種技術，操作起來非常有趣。",
    "#b海盜#k以出色的敏捷性和力量為基礎，向敵人發射百發百中的槍，或用體術將敵人瞬間消滅。槍手可以按照屬性選擇槍彈，有效地進行攻擊，或在船上進行更強大的攻擊，打手可由變身來發揮強有力的體術。",
}
local where = {
    "#b10級#k時請到#b勇士之村#k找#b武術教練#k轉職哦！",
    "#b8級#k時請到#b魔法森林#k找#b漢斯#k轉職哦！",
    "#b10級#k時請到#b弓箭手村#k找#b赫麗娜#k轉職哦！",
    "#b10級#k時請到#b墮落城市#k找#b達克魯#k轉職哦！",
    "#b10級#k時請到#b鯨魚號#k找#b卡伊琳#k轉職哦！",
}
npc:say(about[sel + 1])
npc:exchange({ quest_data = { { id = 1406, data = tostring(sel + 1) } } })
npc:quest_complete(1400)
npc:say(where[sel + 1], false, false)
