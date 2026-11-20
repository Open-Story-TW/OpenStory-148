package pirate

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// attack is one pirate attack skill. The native opcode follows Java Legacy:
// Grenade, Barrel Bomb and Corkscrew Blow are parsed in
// parseCloseRangeAttack and Rapid Fire and Monkey Wave in parseRangedAttack
// (DamageParse.java); the rest follow the weapon (knuckle close range, gun and
// cannon ranged). The wire quirks of those skills are built into the S0b codec.
type attack struct {
	id   int
	kind combat.AttackKind
	// noAmmo marks ranged skills that consume no bullet (cannon, Energy Orb,
	// Battleship, Air Strike).
	noAmmo bool
	// onHit is the Java MapleStatEffect monster status, applied with the WZ
	// prop and time.
	onHit   []combat.MobStatusApply
	prepare func(*combat.AttackContext) error
	drain   func(*combat.AttackContext, int64) (int, int)
	// onAttack reacts to the owner's applied attacks (skills.Handler.OnAttack).
	onAttack func(*skills.TriggerContext) *skills.BuffChange
	// levelFrom names the learned skill a hidden follow-up is cast at the
	// level of (Handler.LevelFrom): the client sends the follow-up's own id,
	// which no character ever learns.
	levelFrom int
}

var (
	stun = []combat.MobStatusApply{{Stat: combat.MobStun}}
	// Blast Back slows by z (negative) percent.
	slow = []combat.MobStatusApply{{Stat: combat.MobSpeed, ValueFrom: "z"}}
)

var attacks = []attack{
	// 海盜 500
	{id: 5001001, kind: combat.Melee}, // 衝擊拳 Flash Fist
	{id: 5001002, kind: combat.Melee}, // 旋風斬 Somersault Kick
	{id: 5001003, kind: combat.Shoot}, // 雙子星攻擊 Double Shot
	// 打手 510
	{id: 5101002, kind: combat.Melee, onHit: stun}, // 迴旋肘擊 Backspin Blow
	{id: 5101003, kind: combat.Melee, onHit: stun}, // 昇龍拳 Double Uppercut
	{id: 5101004, kind: combat.Melee},              // 狂暴衝擊 Corkscrew Blow
	// 格鬥家 511
	{id: 5111002, kind: combat.Melee, onHit: stun},        // 能量暴擊 Energy Blast
	{id: 5111004, kind: combat.Melee, drain: energyDrain}, // 損人利己 Energy Drain
	{id: 5111006, kind: combat.Melee},                     // 衝擊波 Shockwave
	// 拳霸 512
	{id: 5121001, kind: combat.Melee},               // 魔龍降臨 Dragon Strike
	{id: 5121002, kind: combat.Shoot, noAmmo: true}, // 元氣彈 Energy Orb
	{id: 5121004, kind: combat.Melee, onHit: stun},  // 閃．爆破 Demolition
	{id: 5121005, kind: combat.Melee, onHit: stun},  // 閃．索命 Snatch
	{id: 5121007, kind: combat.Melee},               // 閃．連殺 Barrage (Java comments out its stun)
	// 槍手 520
	{id: 5201001, kind: combat.Shoot},                  // 散射 Invisible Shot
	{id: 5201002, kind: combat.Melee, prepare: poison}, // 炸彈投擲 Grenade
	{id: 5201004, kind: combat.Shoot, onHit: stun},     // 偽裝射擊 Blank Shot
	{id: 5201006, kind: combat.Shoot},                  // 脫離戰場 Recoil Shot
	// 神槍手 521
	{id: 5210000, kind: combat.Shoot},                         // 3連發 Burst Fire
	{id: 5211004, kind: combat.Shoot, prepare: poison},        // 火焰噴射 Flamethrower
	{id: 5211005, kind: combat.Shoot, prepare: freeze},        // 寒霜噴射 Ice Splitter
	{id: 5211006, kind: combat.Shoot, onAttack: homingBeacon}, // 指定攻擊 Homing Beacon
	// 槍神 522
	{id: 5220011, kind: combat.Shoot, onAttack: homingBeacon}, // 精準砲擊 Bullseye
	{id: 5221003, kind: combat.Shoot, noAmmo: true},           // 海鷗特戰隊 Air Strike
	{id: 5221004, kind: combat.Shoot},                         // 瞬‧迅雷 Rapid Fire
	{id: 5221007, kind: combat.Shoot, noAmmo: true},           // 海盜加農炮 Battleship Cannon
	{id: 5221008, kind: combat.Shoot, noAmmo: true},           // 海盜魚雷 Battleship Torpedo
	// 重砲指揮官 501／530–532
	{id: 5011000, kind: combat.Shoot, noAmmo: true},                            // 加農砲爆彈 Cannon Blaster
	{id: 5011001, kind: combat.Melee},                                          // 加農砲衝擊 Cannon Strike
	{id: 5011002, kind: combat.Shoot, noAmmo: true, onHit: slow},               // 迴避砲擊 Blast Back
	{id: 5301000, kind: combat.Shoot, noAmmo: true},                            // 炸裂彈 Scatter Shot
	{id: 5301001, kind: combat.Melee, onHit: stun, prepare: barrelRoll},        // 火藥桶破壞 Barrel Bomb
	{id: 5300007, kind: combat.Melee, levelFrom: 5301001, prepare: barrelRoll}, // (invisible) Barrel Bomb blast, sent after 5301001
	{id: 5311000, kind: combat.Shoot, noAmmo: true},                            // 加農砲 : 穿刺攻擊 Cannon Spike (v148 0x56, slot 0)
	{id: 5311002, kind: combat.Shoot, noAmmo: true, onHit: stun},               // 猴子的衝擊波 Monkey Wave
	{id: 5311003, kind: combat.Melee},                                          // 加農砲跳躍 Cannon Jump
	{id: 5310009, kind: combat.Melee},                                          // 反擊砲手 Counter Crush
	{id: 5321000, kind: combat.Shoot, noAmmo: true},                            // 加農砲火箭 Cannon Bazooka
	{id: 5321001, kind: combat.Shoot, noAmmo: true},                            // 戰艦鯨魚號 Nautilus Strike
	{id: 5321012, kind: combat.Shoot, noAmmo: true},                            // 加農砲連擊 Cannon Barrage
}

func spec(a attack) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: a.kind, OnHit: a.onHit, Prepare: a.prepare, Drain: a.drain, LevelFrom: a.levelFrom}
	if a.noAmmo {
		s.Ammo = combat.AmmoNone
	}
	return s
}

// poison is Java isPoison (dot and dotTime): POISON for max(time, dotTime).
func poison(ctx *combat.AttackContext) error {
	d := max(ctx.Effect.Time, ctx.Effect.DOTTime)
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobPoison, Poison: true, Duration: d})
	return nil
}

// freeze is Java's FREEZE with the doubled duration of freezing skills.
func freeze(ctx *combat.AttackContext) error {
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobFreeze, Duration: 2 * ctx.Effect.Time})
	return nil
}

// barrelRollDistance is Barrel Bomb's WZ x (5301001 common/x): how far the
// barrel rolls before it bursts.
const barrelRollDistance = 350

// barrelRoll widens the WZ damage box for Barrel Bomb and Tornado Upper.
// These projectiles travel up to x pixels before scoring hits. The packet
// carries the caster position and the WZ box describes the projectile itself.
func barrelRoll(ctx *combat.AttackContext) error {
	roll := ctx.Effect.X
	if roll <= 0 {
		roll = barrelRollDistance
	}
	if !ctx.Effect.Box.Set {
		return nil
	}
	ctx.Effect.Box.LT.X -= roll
	ctx.Effect.Box.RB.X += roll
	return nil
}

// energyDrain heals x percent of the damage dealt, at most half of MaxHP
// (Java MapleCharacter afterAttack).
func energyDrain(ctx *combat.AttackContext, dealt int64) (int, int) {
	return int(min(dealt*int64(ctx.Effect.X)/100, int64(ctx.Character.MaxHP/2))), 0
}

// homingBeacon (5211006, 5220011) marks the first monster the skill hit:
// HOMING_BEACON holds its object ID until replaced (Java setLinkMid and
// giveHoming, duration 2100000000 ms).
func homingBeacon(c *skills.TriggerContext) *skills.BuffChange {
	if c.Attack == nil || c.Attack.SkillID != c.Skill.ID || len(c.Attack.Targets) == 0 {
		return nil
	}
	return &skills.BuffChange{Buff: &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "HOMING_BEACON", Value: int(c.Attack.Targets[0])}}}}
}

func init() {
	for _, a := range attacks {
		kind := skills.KindAttack
		if a.onHit != nil || a.prepare != nil {
			kind |= skills.KindMobStatus
		}
		skills.Register(skills.Handler{ID: a.id, Kind: kind, Attack: spec(a), OnAttack: a.onAttack, LevelFrom: a.levelFrom})
	}
}
