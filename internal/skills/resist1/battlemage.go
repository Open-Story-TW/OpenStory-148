package resist1

import (
	"errors"
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"time"
)

// Battle Mage 3200–3212.

// staffAttack is the input family of Battle Mage attacks. The v148 client
// sends Triple Blow as a close-range attack (0x55).
const staffAttack = combat.Melee | combat.Magic

// The four v148 staff-chain skills enter the same branch at 0x93AA21.
// The melee sender uses the magic routine at 0x91D3FF for their mage job;
// Skill.wz marks all four magicDamage=1.
var magicStaffChain = map[int]bool{32001000: true, 32101000: true, 32111002: true, 32121002: true}

func init() {
	magic := func(id int, onHit []combat.MobStatusApply, steps ...func(*combat.AttackContext) error) {
		kind := skills.KindAttack
		if onHit != nil {
			kind |= skills.KindMobStatus
		}
		skills.Register(skills.Handler{ID: id, Kind: kind, Attack: &combat.AttackSpec{
			Kind: staffAttack, MagicDamage: magicStaffChain[id], OnHit: onHit, Prepare: prepare(steps...),
		}})
	}
	magic(32001000, nil)                 // 三重之矛 Triple Blow
	magic(32101000, nil)                 // 四重攻擊 Quad Blow
	magic(32111002, nil)                 // 絕命攻擊 Quintuple Blow
	magic(32121002, nil)                 // 終極攻擊 Finishing Blow
	magic(32101001, stun)                // 神聖黑色鎖鏈 Dark Chain
	magic(32111003, nil, darkShockBonus) // 黑暗閃電 Dark Shock (seed marks it passive; WZ type 1)
	// 最終攻擊 Final Attack: the client fires one of 32001008–11 after a
	// spear attack; 32001001 itself is never cast (Java refuses it).
	for _, id := range []int{32001008, 32001009, 32001010, 32001011} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: staffAttack, LevelFrom: 32001001}})
	}
	// 黑暗世紀 Dark Genesis: also a passive damR on Dark Shock (darkShockBonus).
	skills.Register(skills.Handler{ID: 32121004, Kind: skills.KindAttack | skills.KindMobStatus | skills.KindPassive, Attack: &combat.AttackSpec{Kind: staffAttack, OnHit: stun}})

	skills.Register(skills.Handler{ID: 32001002, Kind: skills.KindSpecial, Cast: costOnly})                        // 瞬間移動 Teleport
	skills.Register(skills.Handler{ID: 32111010, Kind: skills.KindBuff | skills.KindAttack, Cast: teleportMastery, // 瞬間移動精通 Teleport Mastery
		Attack: &combat.AttackSpec{Kind: combat.Magic | combat.Body, Prepare: requireStat("TELEPORT_MASTERY", 32111010)}})
	skills.Register(skills.Handler{ID: 32121003, Kind: skills.KindBuff | skills.KindAttack, Cast: cyclone, // 颶風 Cyclone
		Attack: &combat.AttackSpec{Kind: combat.Body | combat.Magic, Prepare: requireStat("TORNADO", 32121003)}})
	skills.Register(skills.Handler{ID: 32101004, Kind: skills.KindBuff, Cast: value("COMBO_DRAIN", "x")})                      // 紅色吸血術 Blood Drain
	skills.Register(skills.Handler{ID: 32101005, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})                          // 長杖極速 Staff Booster
	skills.Register(skills.Handler{ID: 32111004, Kind: skills.KindBuff, Cast: value("CONVERSION", "x")})                       // 轉換 Conversion
	skills.Register(skills.Handler{ID: 32111014, Kind: skills.KindBuff, Cast: value("STANCE", "prop")})                        // 防禦姿態 Stance
	skills.Register(skills.Handler{ID: 32111006, Kind: skills.KindBuff | skills.KindSummon, Cast: reaper, Attack: reaperSpec}) // 甦醒 Summon Reaper Buff
	skills.Register(skills.Handler{ID: 32121007, Kind: skills.KindPartyBuff, Cast: party(value("MAPLE_WARRIOR", "x"))})
	skills.Register(skills.Handler{ID: 32121008, Kind: skills.KindBuff, Cast: heroesWill})
	// 魔法屏障 Party Shield: Java MapleMist isPoisonMist 2, an invincible area
	// for the owner's party like Smoke Screen.
	skills.Register(skills.Handler{ID: 32121006, Kind: skills.KindFieldObject, Cast: costOnly,
		Attack: &combat.AttackSpec{Field: &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaInvincible, PlayersInvincible: true}}})

	passive := func(id int, f func(*skills.PassiveContext) skills.Bonus) {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
	}
	passive(32100006, func(c *skills.PassiveContext) skills.Bonus { // 長杖精通: Java magic += x (staves skip accuracy)
		if weaponType(c.Character) != staff {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.Values["mastery"], MATK: c.Values["x"], Critical: c.Values["cr"]}
	})
	passive(32100007, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{INT: c.Values["intX"]} }) // 智慧昇華
	passive(32110001, func(c *skills.PassiveContext) skills.Bonus {                                               // 戰鬥精通
		return skills.Bonus{DamagePercent: c.Values["damR"], BossDamagePercent: c.Values["damR"], CriticalDamageMin: c.Values["criticaldamageMin"]}
	})
	passive(32120009, func(c *skills.PassiveContext) skills.Bonus { // 勁能 (Energize)
		return skills.Bonus{
			MaxHPPercent: c.Values["mhpR"], MaxMPPercent: c.Values["mmpR"],
			WDEFPercent: c.Values["pddR"], MDEFPercent: c.Values["mddR"],
		}
	})
	combat.ObserveAttacks(reaperSpawn)
	// 最終攻擊 (linked by 32001008–11) and the PvP-only 少量消弱.
	for _, id := range []int{32001001, 32000012} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive})
	}
}

// darkShockBonus adds Dark Genesis' passive damR to Dark Shock.
func darkShockBonus(ctx *combat.AttackContext) error {
	if lv := learnedLevel(ctx.Character, 32121004, ctx.Now); lv > 0 {
		v, err := skillValues(32121004, lv)
		if err != nil {
			return err
		}
		ctx.Effect.Damage = ctx.Effect.Damage * (100 + v["damR"]) / 100
	}
	return nil
}

// requireStat refuses an attack (the 0x4E body attack in particular) unless
// the caster runs the skill's buff (Java PlayerHandler CP_UserBodyAttack),
// and makes those hits free.
func requireStat(name string, id int) func(*combat.AttackContext) error {
	return func(ctx *combat.AttackContext) error {
		if _, ok := activeRow(ctx.Character, id, name, ctx.Now); !ok {
			return errors.New("attack needs its buff")
		}
		// The cast paid the MP; the hits while the buff runs are free
		// (MCP 2026-11-02: each Cyclone body attack charged mpCon again).
		ctx.Effect.MPCon = 0
		return nil
	}
}

// teleportMastery (32111010) toggles TELEPORT_MASTERY x until recast (Java
// 2100000000 ms) and charges y MP instead of a mpCon.
func teleportMastery(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Value("x") < 1 {
		return skills.CastPlan{}, fmt.Errorf("skill32111010: no x")
	}
	return skills.CastPlan{MPCost: c.Value("y"), Toggle: true, Self: &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{stat("TELEPORT_MASTERY", c.Value("x"))}}}, nil
}

// cyclone (32121003): TORNADO x for time; the spin hits arrive as body
// attacks while it runs.
func cyclone(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("TORNADO", c.Value("x")))
}

// reaper (32111006) is the REAPER buff; the reapers spawn from attacks while
// it runs (reaperSpawn).
func reaper(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("REAPER", 1))
}

// reaperSpec is the reaper summon: walking stationary (Java
// WALK_STATIONARY 2), several at once (isMultiSummon), spawn stance 5 (Java
// spawnSummon writes 5 for 32111006), attacking, living x seconds; the cast
// itself spawns none (Java applyTo returns before the summon when primary).
var reaperSpec = &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: 2, AssistType: 1, Stance: 5, Multi: true, NoCastSpawn: true, DurationKey: "x"}}

// maxReapers is Java afterAttack's getSummonsSize() < 4.
const maxReapers = 4

// reaperSpawn is Java afterAttack for REAPER: for each damaged monster of
// the owner's own attack, prop percent to summon a reaper while fewer than
// four are out.
func reaperSpawn(e combat.AttackEvent) {
	if e.Summon || e.SpawnSummon == nil || e.SummonCount == nil || len(e.Targets) == 0 {
		return
	}
	row, ok := activeRow(e.Session.Character, 32111006, "REAPER", e.Now)
	if !ok {
		return
	}
	v, err := skillValues(32111006, row.Level)
	if err != nil {
		return
	}
	for range e.Targets {
		if e.SummonCount(32111006) >= maxReapers {
			return
		}
		if rollProp(v["prop"]) {
			_ = e.SpawnSummon(32111006, row.Level)
		}
	}
}

// time helper kept local to the auras.
func seconds(n int) time.Duration { return time.Duration(n) * time.Second }
