package pirate

import (
	"encoding/hex"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func values(t *testing.T, id, level int) (content.Skill, map[string]int) {
	t.Helper()
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return def, v
}

func cast(t *testing.T, id, level int) skills.CastPlan {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, v := values(t, id, level)
	caster := domain.Character{HP: 5000, MaxHP: 5000, MP: 5000, MaxMP: 5000}
	switch id {
	case 5120012:
		caster.Skills = []domain.SkillState{{ID: 5111007, Level: 20}}
	case 5220014:
		caster.Skills = []domain.SkillState{{ID: 5211007, Level: 20}}
	}
	p, err := h.Cast(&skills.CastContext{Skill: def, Level: level, Values: v, Caster: caster, Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func stats(b *skills.Buff) map[buffstat.Stat]int {
	out := map[buffstat.Stat]int{}
	for _, s := range b.Stats {
		out[s.Stat] = s.Value
	}
	return out
}

func TestV148GunExpertAcceptsTheAmmoFreeFollowUp(t *testing.T) {
	h, ok := skills.Lookup(5220020)
	if !ok || h.Attack == nil || h.Passive == nil {
		t.Fatal("Gun Expert lacks its passive or final attack")
	}
	def, _ := values(t, 5220020, 30)
	effect, err := combat.FromSkill(def, 30)
	if err != nil {
		t.Fatal(err)
	}
	p, err := combat.PlanAttack(h.Attack, combat.AttackContext{
		Kind: combat.Shoot, Effect: effect,
		Character: domain.Character{Job: 522, Equipment: []domain.Equipment{{ItemID: 1492004, Slot: -11}}},
		Stats:     combat.PlayerStats{Job: 522, WeaponID: 1492004},
	}, nil)
	if err != nil || p.Cost.Bullets != 0 || effect.Damage != 70 {
		t.Fatalf("Gun Expert final attack plan %+v, effect damage %d, error %v", p, effect.Damage, err)
	}
}

func TestV148RegionalMindControlConsumesABulletAndDazzlesOnHit(t *testing.T) {
	const id = 5921009
	// Original v148 frame captured from a level-20 Mind Control on a living normal mob.
	const wire = "56000111f1585a0014031e24fe00007a00c46c6fb10904d47671010000000005000000005e420f00070100706f98000191ffd70091ffd7006501c00a00005d2b923e02d552f331ffd70031ffbb00"
	data, err := hex.DecodeString(wire)
	if err != nil {
		t.Fatal(err)
	}
	attack, err := protocol.ReadAttack(protocol.RecvShootAttack, protocol.NewReader(data[2:]))
	if err != nil || attack.SkillID != id || attack.Slot != 5 || attack.CSStar != 0 || len(attack.Targets) != 1 {
		t.Fatalf("v148 Mind Control attack %+v, err %v", attack, err)
	}
	def, _ := values(t, id, 20)
	effect, err := combat.FromSkill(def, 20)
	if err != nil || effect.Range != 350 || effect.Prop != 94 || effect.Time != 40*time.Second {
		t.Fatalf("v148 WZ effect %+v, err %v", effect, err)
	}
	now := time.Unix(100, 0)
	ctx := combat.AttackContext{
		Kind: combat.Shoot, Attack: attack, Effect: effect, Now: now,
		Character: domain.Character{ID: 4911, Job: 592, MP: 1000, Equipment: []domain.Equipment{{ItemID: 1492004, Slot: -11}}},
		Stats:     combat.PlayerStats{WeaponID: 1492004, STR: 100, DEX: 500, WATK: 200},
		Targets:   []combat.TargetInfo{{ObjectID: attack.Targets[0].ObjectID, X: int(attack.X) + 96, Y: int(attack.Y), HP: 1_000_000}},
	}
	h, ok := skills.Lookup(id)
	if !ok || h.Attack == nil {
		t.Fatal("Mind Control attack is not registered")
	}
	spec := h.Attack
	plan, err := combat.PlanAttack(spec, ctx, func(prop int) bool { return prop == 94 })
	if err != nil || plan.Cost.Ammo != combat.AmmoBullet || plan.Cost.Bullets != 1 || plan.Cost.MP != 50 || len(plan.Targets) != 1 {
		t.Fatalf("ordinary bullet and MP cost %+v, targets %+v, err %v", plan.Cost, plan.Targets, err)
	}
	status := plan.Targets[0].Statuses
	if len(status) != 1 || status[0].Stat != combat.MobDazzle || status[0].Value != int32(ctx.Character.ID) || status[0].SkillID != id ||
		status[0].OwnerID != ctx.Character.ID || !status[0].ExpiresAt.Equal(now.Add(40*time.Second)) {
		t.Fatalf("wrong native Mind Control status %+v", status)
	}
	s := status[0]
	wireStatus := protocol.ApplyMonsterStatus(attack.Targets[0].ObjectID, []protocol.MobStatValue{{Stat: s.Stat, Value: s.Value, Reason: s.SkillID, ExpiresAt: s.ExpiresAt}}, 0, now)
	r := protocol.NewReader(wireStatus)
	if r.U16() != protocol.SendApplyMonsterStatus || r.I32() != attack.Targets[0].ObjectID {
		t.Fatalf("wrong monster in original status packet %x", wireStatus)
	}
	for i := 0; i < 7; i++ {
		if r.U32() != 0 {
			t.Fatalf("unexpected upper mask word in original status packet %x", wireStatus)
		}
	}
	if r.U32() != 1<<28 || r.I32() != int32(ctx.Character.ID) || r.I32() != id || r.U16() != 80 || r.Err() != nil {
		t.Fatalf("wrong original Mind Control owner, skill, or expiry in %x", wireStatus)
	}
	ctx.Targets[0].Mob.Boss = true
	boss, err := combat.PlanAttack(spec, ctx, func(int) bool { return true })
	if err != nil || len(boss.Targets) != 1 || len(boss.Targets[0].Statuses) != 0 {
		t.Fatalf("boss should take the shot without becoming controllable: %+v, err %v", boss.Targets, err)
	}
	ctx.Character.Equipment[0].ItemID = 1482001
	if _, err = combat.PlanAttack(spec, ctx, nil); err == nil {
		t.Fatal("Mind Control accepted a non-gun weapon")
	}
}

// Every registered pirate skill is in the WZ seed, every castable one plans
// at every level, and every stat it plans is a known buff stat.
func TestEveryPirateHandlerPlansKnownStats(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 2 }
	count := 0
	for _, id := range skills.IDs() {
		if skills.OwnerOf(id) != "pirate" {
			continue
		}
		count++
		h, _ := skills.Lookup(id)
		def, err := content.SkillDefinition("", id)
		if err != nil {
			t.Fatalf("%d: %v", id, err)
		}
		if h.Attack != nil && h.Attack.MobCast == nil && id != 5221009 && id != 5921009 { // Mind Control has no WZ damage (combat defaults to 100%).
			if _, v := values(t, id, def.MaxLevel); v["damage"] < 1 {
				t.Errorf("%d: attack without damage", id)
			}
		}
		if h.Cast == nil {
			continue
		}
		for level := 1; level <= def.MaxLevel; level++ {
			p := cast(t, id, level)
			if p.MPCost < 0 || p.HPCost < 0 || p.Cooldown < 0 {
				t.Errorf("%d@%d: negative cost %+v", id, level, p)
			}
			buff := p.Self
			if buff == nil {
				buff = p.Party
			}
			effect := p.Dispel || p.Heal != nil || p.ResetCooldowns || h.Kind&skills.KindSpecial != 0 || h.Attack != nil && h.Attack.MobCast != nil
			if buff == nil && !effect || buff != nil && len(buff.Stats) == 0 && !effect || buff != nil && len(buff.Stats) > 0 && buff.Duration < time.Second {
				t.Fatalf("%d@%d: plan does nothing %+v", id, level, p)
			}
			if buff == nil {
				continue
			}
			if (h.Kind&skills.KindPartyBuff != 0) != (p.Party != nil) {
				t.Errorf("%d: party kind and plan differ", id)
			}
			for _, s := range buff.Stats {
				if _, ok := buffstat.Lookup(s.Stat); !ok {
					t.Errorf("%d: unknown stat %s", id, s.Stat)
				}
			}
		}
	}
	if count != 196 { // Includes the regional v148 pirate books.
		t.Fatalf("registered %d pirate skills", count)
	}
}

func TestV148PirateRegionalPartyCasts(t *testing.T) {
	for _, id := range []int{5721000, 5821000, 5921000} {
		p := cast(t, id, 30)
		if p.Self != nil || p.Party == nil || p.Area == nil ||
			p.Area.Left != -400 || p.Area.Top != -300 || p.Area.Right != 400 || p.Area.Bottom != 300 ||
			stats(p.Party)["MAPLE_WARRIOR"] != 15 || p.Party.Duration != 900*time.Second || p.MPCost != 70 {
			t.Errorf("%d: Maple Warrior plan %+v, buff %+v", id, p, p.Party)
		}
	}
	for _, tc := range []struct {
		id, level int
		cooldown  time.Duration
	}{{5121010, 5, 1500 * time.Second}, {5821010, 30, 1200 * time.Second}} {
		p := cast(t, tc.id, tc.level)
		if p.Self != nil || p.Party == nil || !p.ResetCooldowns ||
			len(p.KeepCooldowns) != 2 || p.KeepCooldowns[0] != 5121010 || p.KeepCooldowns[1] != 5821010 ||
			p.Cooldown != tc.cooldown || p.Area == nil ||
			p.Area.Left != -400 || p.Area.Top != -300 || p.Area.Right != 400 || p.Area.Bottom != 300 {
			t.Errorf("%d: Time Leap plan %+v", tc.id, p)
		}
	}
}

func TestBulletTimeUsesNativeV148AccuracyAndJump(t *testing.T) {
	b := bulletTime(&skills.PassiveContext{Values: map[string]int{
		"accX": 220, "psdJump": 10, "speedMax": 20,
	}})
	if b != (skills.Bonus{ACC: 220, Jump: 10, SpeedLimitBonus: 20}) {
		t.Fatalf("v148 Bullet Time ignored its WZ values: %+v", b)
	}
}

func TestRegionalMeleeTrainingAffectsOnlyRegionalSkills(t *testing.T) {
	b := passive(t, 5810008, 10, domain.Character{Job: 581})
	want := skills.Bonus{SkillDamage: skillDamage(5801002, 120, 5801003, 90, 5801004, 150)}
	if b != want {
		t.Fatalf("regional melee training: %+v", b)
	}
}

func TestV148PiratesStunCriticalUsesTargetBonus(t *testing.T) {
	for _, row := range []struct {
		id     int
		values map[string]int
		want   int
	}{
		{5110000, map[string]int{"prop": 60, "subProp": 20}, 60},
		{5810000, map[string]int{"prop": 30}, 30},
	} {
		h, ok := skills.Lookup(row.id)
		if !ok || h.Passive == nil {
			t.Fatalf("skill %d is missing a passive handler", row.id)
		}
		if b := h.Passive(&skills.PassiveContext{Values: row.values}); b != (skills.Bonus{StunnedCritical: row.want}) {
			t.Fatalf("skill %d target critical: %+v", row.id, b)
		}
	}
}

func TestV148PiratesRevengeUsesNativeAdditiveDamageStat(t *testing.T) {
	defer func(previous func(int) int) { rollDie = previous }(rollDie)
	rollDie = func(int) int { return 40 }
	now := time.Unix(100, 0)
	for _, id := range []int{5120011, 5220012, 5720012, 5820011, 5920012} {
		h, ok := skills.Lookup(id)
		if !ok || h.OnDamaged == nil || h.Passive == nil {
			t.Fatalf("Pirate's Revenge %d is incomplete", id)
		}
		def := content.Skill{ID: id}
		c := domain.Character{Job: id / 10000}
		result := h.OnDamaged(&skills.TriggerContext{
			Skill: def, Character: c, Damage: 100, Now: now,
			Values: map[string]int{"indieDamR": 15, "time": 45, "x": 50, "prop": 40},
		})
		want := skills.StatValue{Stat: "PIRATES_REVENGE", Value: 15}
		if result == nil || result.Buff == nil || result.Buff.Duration != 45*time.Second ||
			result.Cooldown != 50*time.Second || len(result.Buff.Stats) != 1 || result.Buff.Stats[0] != want {
			t.Fatalf("Pirate's Revenge %d: %+v", id, result)
		}
		c.SkillBuffs = []domain.SkillBuff{{ID: id, Stat: string(want.Stat), Value: want.Value, ExpiresAt: now.Add(time.Second)}}
		wire := protocol.NewReader(protocol.SkillBuff(c.SkillBuffs[0], now))
		if opcode := wire.U16(); opcode != 0x2d {
			t.Fatalf("Pirate's Revenge %d opcode 0x%x", id, opcode)
		}
		for word := 7; word >= 0; word-- {
			var mask uint32
			if word == 116/32 {
				mask = 1 << (116 % 32)
			}
			if got := wire.U32(); got != mask {
				t.Fatalf("Pirate's Revenge %d stat mask word %d: 0x%x, want 0x%x", id, word, got, mask)
			}
		}
		if value, source, duration := wire.I16(), wire.I32(), wire.I32(); value != 15 || source != int32(id) || duration != 1000 {
			t.Fatalf("Pirate's Revenge %d triplet %d/%d/%d", id, value, source, duration)
		}
		if b := h.Passive(&skills.PassiveContext{Skill: def, Character: c, Now: now}); b.DamagePercent != 15 {
			t.Fatalf("Pirate's Revenge bonus from %d: %+v", id, b)
		}
		if b := h.Passive(&skills.PassiveContext{Skill: def, Character: c, Now: now.Add(time.Second)}); b.DamagePercent != 0 {
			t.Fatalf("Pirate's Revenge expired bonus from %d: %+v", id, b)
		}
	}
}

func TestV148RegionalPirateAttacks(t *testing.T) {
	for _, row := range []struct {
		id     int
		kind   combat.AttackKind
		charge bool
		stun   bool
	}{
		{5081001, combat.Melee, false, false},
		{5081002, combat.Shoot, false, false},
		{5081005, combat.Melee, false, false},
		{5091003, combat.Shoot, false, false},
		{5700004, combat.Shoot, false, false},
		{5121016, combat.Shoot, false, false},
		{5721001, combat.Shoot, false, false},
		{5721007, combat.Shoot, false, false},
		{5801002, combat.Melee, false, true},
		{5801003, combat.Melee, false, true},
		{5801004, combat.Melee, true, false},
		{5811002, combat.Melee, false, true},
		{5811004, combat.Melee, false, false},
		{5821004, combat.Melee, false, false},
		{5901002, combat.Melee, true, false},
		{5901004, combat.Melee, false, true},
		{5901006, combat.Shoot, false, false},
		{5910000, combat.Shoot, false, false},
		{5911005, combat.Shoot, false, false},
		{5911006, combat.Shoot, false, false},
		{5921007, combat.Shoot, false, false},
		{5921008, combat.Shoot, false, false},
	} {
		h, ok := skills.Lookup(row.id)
		if !ok || h.Attack == nil || h.Attack.Kind != row.kind || protocol.QuirkFor(row.id).Charge != row.charge {
			t.Fatalf("wrong v148 attack family or charge layout for %d", row.id)
		}
		gotStun := len(h.Attack.OnHit) == 1 && h.Attack.OnHit[0].Stat == combat.MobStun
		if gotStun != row.stun {
			t.Fatalf("wrong v148 stun assignment for %d", row.id)
		}
	}
	now := time.Unix(100, 0)
	ctx := &combat.AttackContext{Now: now}
	regionalEnergyReady := requiresAttackBuff(5810001, "ENERGY_CHARGE", energyFull)
	regionalBattleshipReady := requiresAttackBuff(5921006, "MONSTER_RIDING", battleshipMount)
	if regionalEnergyReady(ctx) == nil {
		t.Fatal("energy attack succeeds with no charge")
	}
	ctx.Character.SkillBuffs = []domain.SkillBuff{{ID: 5110001, Stat: "ENERGY_CHARGE", Value: energyFull, ExpiresAt: now.Add(time.Minute)}}
	if regionalEnergyReady(ctx) == nil {
		t.Fatal("energy attack accepts another job's gauge")
	}
	ctx.Character.SkillBuffs[0].ID = 5810001
	if err := regionalEnergyReady(ctx); err != nil {
		t.Fatal(err)
	}
	if regionalBattleshipReady(ctx) == nil {
		t.Fatal("battleship attack succeeds without its active mount")
	}
	ctx.Character.SkillBuffs[0] = domain.SkillBuff{ID: 5921006, Stat: "MONSTER_RIDING", Value: battleshipMount, ExpiresAt: now.Add(time.Minute)}
	if err := regionalBattleshipReady(ctx); err != nil {
		t.Fatal(err)
	}
	morphAttack, _ := skills.Lookup(5821004)
	ctx.Character.SkillBuffs[0] = domain.SkillBuff{ID: 5811005, Stat: "MORPH", Value: 1001, ExpiresAt: now.Add(time.Minute)}
	if morphAttack.Attack.Prepare(ctx) == nil {
		t.Fatal("Demolition accepts an unrelated transform source")
	}
	ctx.Character.SkillBuffs[0].ID = 5821003
	if err := morphAttack.Attack.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	ctx.Character.SkillBuffs[0].ExpiresAt = now
	if morphAttack.Attack.Prepare(ctx) == nil {
		t.Fatal("Demolition accepts an expired transform")
	}
	h, _ := skills.Lookup(5911005)
	def, _ := values(t, 5911005, 20)
	e, err := combat.FromSkill(def, 20)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := combat.PlanAttack(h.Attack, combat.AttackContext{
		Kind: combat.Shoot, Effect: e, Character: domain.Character{MP: 1000}, Stats: combat.PlayerStats{WeaponID: 1492004},
	}, nil)
	if err != nil || plan.Cost.Items != 0 || plan.Cost.Ammo != combat.AmmoBullet || plan.Cost.Bullets != 1 {
		t.Fatalf("frost shot uses the native bullet slot: %+v (%v)", plan.Cost, err)
	}
	for _, id := range []int{5081002, 5700004, 5721007} {
		h, _ = skills.Lookup(id)
		if h.Attack.Ammo != combat.AmmoNone {
			t.Fatalf("%d native shot does not nominate a projectile stack", id)
		}
	}
}

func TestV148RegionalPiratePassives(t *testing.T) {
	gunUser := domain.Character{Equipment: []domain.Equipment{{ItemID: 1492004, Slot: -11}}}
	for _, tc := range []struct {
		id   int
		want skills.Bonus
	}{
		{5080004, skills.Bonus{Critical: 20, CriticalDamageMin: 10}},
		{5090000, skills.Bonus{ACC: 160, AVOID: 160}},
		{5700000, skills.Bonus{Mastery: 50, ACC: 120}},
		{5700003, skills.Bonus{STR: 30, DEX: 30}},
		{5800008, skills.Bonus{Critical: 20, CriticalDamageMin: 10}},
		{5800009, skills.Bonus{MaxHPPercent: 20}},
		{5900000, skills.Bonus{Mastery: 50, ACC: 120}},
		{5900007, skills.Bonus{Critical: 20, CriticalDamageMin: 10}},
	} {
		def, err := content.SkillDefinition("", tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if got := passive(t, tc.id, def.MaxLevel, gunUser); got != tc.want {
			t.Errorf("%d got %+v, want %+v", tc.id, got, tc.want)
		}
	}
	knuckleUser := domain.Character{Equipment: []domain.Equipment{{ItemID: 1482000, Slot: -11}}}
	if got := passive(t, 5800001, 20, knuckleUser); got != (skills.Bonus{Mastery: 50, ACC: 120}) {
		t.Errorf("knuckle mastery: %+v", got)
	}
	if got := passive(t, 5900000, 20, knuckleUser); got != (skills.Bonus{}) {
		t.Errorf("gun mastery applied with a knuckle: %+v", got)
	}
}

func TestBoosterMapleWarriorAndSpeedInfusion(t *testing.T) {
	p := cast(t, 5101006, 20)
	if p.MPCost != 11 || p.Self.Duration != 200*time.Second || stats(p.Self)["BOOSTER"] != -2 {
		t.Fatalf("knuckle booster %+v %+v", p, p.Self)
	}
	for _, id := range []int{5701005, 5901003} {
		p = cast(t, id, 20)
		if p.MPCost != 11 || p.Self.Duration != 200*time.Second || stats(p.Self)["BOOSTER"] != -2 {
			t.Fatalf("regional gun booster %d: %+v %+v", id, p, p.Self)
		}
	}
	p = cast(t, 5221000, 30)
	if p.Self != nil || p.Party == nil || p.Area == nil || p.Area.Left >= 0 || stats(p.Party)["MAPLE_WARRIOR"] != 15 || p.Party.Duration != 900*time.Second || p.MPCost != 70 {
		t.Fatalf("maple warrior %+v %+v", p, p.Party)
	}
	p = cast(t, 5121009, 20)
	if p.Party == nil || stats(p.Party)["SPEED_INFUSION"] != -2 || p.MPCost != 40 || p.HPCost != 40 {
		t.Fatalf("speed infusion %+v", p)
	}
	p = cast(t, 5321010, 30)
	if stats(p.Self)["STANCE"] != 15 || p.Self.Duration != 300*time.Second {
		t.Fatalf("pirate's spirit %+v", p.Self)
	}
}

func TestPiratesRevengeTriggersOnDamage(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	h, _ := skills.Lookup(5120011)
	if h.Cast != nil || h.OnDamaged == nil {
		t.Fatal("revenge is a damage trigger, not a cast")
	}
	def, v := values(t, 5120011, 10)
	now := time.Unix(1000, 0)
	ctx := func(job, damage int, rows []domain.SkillBuff, cds []domain.SkillCooldown) *skills.TriggerContext {
		return &skills.TriggerContext{Skill: def, Level: 10, Values: v, Character: domain.Character{Job: job, SkillCooldowns: cds}, Rows: rows, Damage: damage, Now: now}
	}
	rollDie = func(int) int { return 40 } // prop 40 at level 10
	c := h.OnDamaged(ctx(512, 100, nil, nil))
	if c == nil || c.Cooldown != 50*time.Second || c.Buff.Duration != 45*time.Second || c.Buff.Stats[0] != (skills.StatValue{Stat: "PIRATES_REVENGE", Value: 15}) {
		t.Fatalf("revenge %+v", c)
	}
	for name, x := range map[string]*skills.TriggerContext{
		"wrong job": ctx(522, 100, nil, nil),
		"no damage": ctx(512, 0, nil, nil),
		"active":    ctx(512, 100, []domain.SkillBuff{{ID: 5120011, Stat: "PIRATES_REVENGE", Value: 15, ExpiresAt: now.Add(time.Second)}}, nil),
		"cooling":   ctx(512, 100, nil, []domain.SkillCooldown{{ID: 5120011, ExpiresAt: now.Add(time.Second)}}),
	} {
		if c := h.OnDamaged(x); c != nil {
			t.Errorf("%s triggered", name)
		}
	}
	rollDie = func(int) int { return 41 }
	if c := h.OnDamaged(ctx(512, 100, nil, nil)); c != nil {
		t.Fatal("prop roll failed but triggered")
	}
}

func TestDashAndMorphs(t *testing.T) {
	p := cast(t, 5001005, 10)
	if s := stats(p.Self); s["DASH_SPEED"] != 30 || s["DASH_JUMP"] != 20 || p.Self.Duration != 20*time.Second || p.MPCost != 5 {
		t.Fatalf("dash %+v %+v", p, p.Self)
	}
	p = cast(t, 5091005, 10)
	if s := stats(p.Self); len(s) != 2 || s["DASH_SPEED"] != 30 || s["DASH_JUMP"] != 20 || p.Self.Duration != 20*time.Second || p.MPCost != 5 {
		t.Fatalf("regional dash %+v %+v", p, p.Self)
	}
	p = cast(t, 5111005, 20)
	if p.Self.Stats[0] != (skills.StatValue{Stat: "STANCE", Value: 100}) {
		t.Fatalf("transformation must lead with STANCE: %+v", p.Self.Stats)
	}
	if s := stats(p.Self); s["MORPH"] != 1000 || s["ENHANCED_WATK"] != 20 || s["ENHANCED_WDEF"] != 200 || s["ENHANCED_MDEF"] != 200 || s["SPEED"] != 40 || s["JUMP"] != 20 || p.Cooldown != 380*time.Second {
		t.Fatalf("transformation %+v", s)
	}
	p = cast(t, 5121003, 20)
	if s := stats(p.Self); s["MORPH"] != 1001 || s["STANCE"] != 100 || s["ENHANCED_WATK"] != 40 {
		t.Fatalf("super transformation %+v", s)
	}
	p = cast(t, 5101007, 10)
	if s := stats(p.Self); s["MORPH"] != 1002 || s["STANCE"] != 0 || len(s) != 1 || p.Cooldown != 10*time.Second {
		t.Fatalf("oak barrel %+v %+v", p, s)
	}
}

func TestMonkeyMagicIndieStats(t *testing.T) {
	p := cast(t, 5301003, 20)
	s := stats(p.Party)
	if s["HP_BOOST"] != 300 || s["MP_BOOST"] != 300 || s["ANGEL_ACC"] != 160 || s["ANGEL_AVOID"] != 160 || s["ANGEL_JUMP"] != 5 || s["ANGEL_SPEED"] != 10 || s["ANGEL_STAT"] != 20 || p.Party.Duration != 300*time.Second {
		t.Fatalf("monkey magic %+v", s)
	}
}

func TestDiceRolls(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 1 }
	if p := cast(t, 5211007, 20); p.Self != nil || p.MPCost != 60 || p.Cooldown != 200*time.Second {
		t.Fatalf("a one has no buff but costs: %+v", p)
	}
	rollDie = func(int) int { return 4 }
	if p := cast(t, 5311004, 10); stats(p.Self)["DICE_ROLL"] != 4 || p.Self.Duration != 240*time.Second {
		t.Fatalf("barrel %+v", p.Self)
	}
	rolls := []int{3, 5, 3} // first die, prop roll, second die
	rollDie = func(int) int { v := rolls[0]; rolls = rolls[1:]; return v }
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 300 {
		t.Fatalf("double down %+v", p.Self)
	}
	rolls = []int{6, 100, 0}
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 6 {
		t.Fatalf("single die %+v", p.Self)
	}
	rolls = []int{1, 1, 4}
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 4 {
		t.Fatalf("second die only %+v", p.Self)
	}
	rolls = []int{2, 1, 5}
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 25 {
		t.Fatalf("two dice %+v", p.Self)
	}
}

func passive(t *testing.T, id, level int, c domain.Character) skills.Bonus {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Passive == nil {
		t.Fatalf("%d has no passive", id)
	}
	def, v := values(t, id, level)
	return h.Passive(&skills.PassiveContext{Skill: def, Level: level, Values: v, Character: c})
}

func TestMasteryNeedsTheMatchingWeapon(t *testing.T) {
	withWeapon := func(job, item int) domain.Character {
		return domain.Character{Job: job, Equipment: []domain.Equipment{{ItemID: item, Slot: -11}}}
	}
	if b := passive(t, 5100001, 20, withWeapon(510, 1482000)); b.Mastery != 50 || b.ACC != 120 {
		t.Fatalf("knuckle mastery %+v", b)
	}
	if b := passive(t, 5100001, 20, withWeapon(510, 1492000)); b != (skills.Bonus{}) {
		t.Fatalf("knuckle mastery with a gun %+v", b)
	}
	if b := passive(t, 5300005, 20, withWeapon(530, 1532000)); b.Mastery != 50 || b.ACC != 120 {
		t.Fatalf("cannon mastery %+v", b)
	}
	if b := passive(t, 5010003, 10, withWeapon(531, 1532000)); b != (skills.Bonus{WATK: 20, WDEF: 250, MDEF: 250}) {
		t.Fatalf("cannon upgrade %+v", b)
	}
	if b := passive(t, 5010003, 10, withWeapon(510, 1482000)); b != (skills.Bonus{}) {
		t.Fatalf("cannon upgrade for a brawler %+v", b)
	}
	if b := passive(t, 5110000, 20, domain.Character{Job: 511}); b.Critical != 30 {
		t.Fatalf("critical punch %+v", b)
	}
	if b := passive(t, 5100009, 10, domain.Character{Job: 510}); b.MaxHPPercent != 20 {
		t.Fatalf("hp increase %+v", b)
	}
	if b := passive(t, 5000000, 20, domain.Character{Job: 500}); b.ACC != 160 || b.AVOID != 160 {
		t.Fatalf("bullet time %+v", b)
	}
}

func TestAttackSpecsCarryAmmoStatusAndDrain(t *testing.T) {
	spec := func(id int) *combat.AttackSpec {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil {
			t.Fatalf("%d is no attack", id)
		}
		return h.Attack
	}
	if s := spec(5001003); s.Kind != combat.Shoot || s.Ammo != combat.AmmoAuto {
		t.Fatalf("double shot %+v", s)
	}
	if s := spec(5321012); s.Kind != combat.Shoot || s.Ammo != combat.AmmoNone {
		t.Fatalf("cannon barrage %+v", s)
	}
	if s := spec(5311000); s.Kind != combat.Shoot || s.Ammo != combat.AmmoNone {
		t.Fatalf("v148 cannon spike uses a ranged frame without an ammo slot: %+v", s)
	}
	if s := spec(5201002); s.Kind != combat.Melee || s.Prepare == nil {
		t.Fatalf("grenade is a close-range charge attack with poison %+v", s)
	}
	if s := spec(5101002); len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobStun {
		t.Fatalf("backspin blow stuns %+v", s.OnHit)
	}
	if s := spec(5011002); len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobSpeed || s.OnHit[0].ValueFrom != "z" {
		t.Fatalf("blast back slows by z %+v", s.OnHit)
	}

	def, _ := values(t, 5211005, 20)
	e, err := combat.FromSkill(def, 20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &combat.AttackContext{Effect: e}
	if err := spec(5211005).Prepare(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 1 || ctx.Effect.MonsterStatus[0].Stat != combat.MobFreeze || ctx.Effect.MonsterStatus[0].Duration != 8*time.Second {
		t.Fatalf("ice splitter freezes twice its time %+v", ctx.Effect.MonsterStatus)
	}
	def, _ = values(t, 5201002, 20)
	if e, err = combat.FromSkill(def, 20); err != nil {
		t.Fatal(err)
	}
	ctx = &combat.AttackContext{Effect: e}
	if err := spec(5201002).Prepare(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 1 || !ctx.Effect.MonsterStatus[0].Poison || ctx.Effect.MonsterStatus[0].Duration != 8*time.Second {
		t.Fatalf("grenade poisons for dotTime %+v", ctx.Effect.MonsterStatus)
	}

	ctx = &combat.AttackContext{Effect: combat.Effect{X: 20}, Character: domain.Character{MaxHP: 1000}}
	if hp, mp := spec(5111004).Drain(ctx, 1000); hp != 200 || mp != 0 {
		t.Fatalf("energy drain %d %d", hp, mp)
	}
	if hp, _ := spec(5111004).Drain(ctx, 100000); hp != 500 {
		t.Fatalf("energy drain is capped at half MaxHP: %d", hp)
	}
}

func TestSummonsAndMindControl(t *testing.T) {
	for _, c := range []struct {
		id    int
		stat  buffstat.Stat
		move  uint8
		multi bool
	}{{5211001, "PUPPET", 0, true}, {5211002, "SUMMON", 5, true}, {5220002, "PUPPET", 0, true}, {5321003, "PUPPET", 0, false}, {5321004, "SUMMON", 1, false}} {
		h, _ := skills.Lookup(c.id)
		s := h.Attack.Summon
		if h.Attack.Kind != combat.Summoned || s == nil || s.MoveAbility != c.move || s.AssistType != 1 || s.Multi != c.multi || s.Puppet {
			t.Fatalf("%d summon %+v", c.id, s)
		}
		_, v := values(t, c.id, 10)
		if p := cast(t, c.id, 10); stats(p.Self)[c.stat] != 1 || p.Self.Duration != time.Duration(v["time"])*time.Second {
			t.Fatalf("%d summon buff %+v", c.id, p.Self)
		}
	}
	stats := combat.PlayerStats{Job: 532, Level: 120, STR: 400, DEX: 100, WATK: 100, WeaponID: 1532000}
	e := combat.Effect{SkillID: 5321003, Level: 15, Damage: 500}
	plain := combat.SummonCeiling(combat.DamageContext{Stats: stats, Effect: e})
	h, _ := skills.Lookup(5321003)
	if got := h.Attack.MaxDamage(combat.DamageContext{Stats: stats, Effect: e}); got <= plain {
		t.Fatalf("anchor ceiling %d must add selfDestruction to %d", got, plain)
	}
	h, _ = skills.Lookup(5221009)
	if h.Cast != nil || h.Attack.MobCast != nil || h.Attack.Kind != combat.Shoot || h.Attack.Ammo != combat.AmmoNone || len(h.Attack.OnHit) != 1 || h.Attack.OnHit[0].Stat != combat.MobDazzle {
		t.Fatalf("mind control is a ranged attack that hypnotizes %+v", h.Attack)
	}
}

func TestSpecialCasts(t *testing.T) {
	for _, id := range []int{5121008, 5721002, 5821008, 5921010} {
		if p := cast(t, id, 5); !p.Dispel || p.Self != nil || p.Party != nil || p.Cooldown != 360*time.Second || p.MPCost != 30 {
			t.Fatalf("maple purify %d %+v", id, p)
		}
	}
	if p := cast(t, 5201005, 10); p.Self != nil || p.Party != nil || p.MPCost != 14 {
		t.Fatalf("wings is cost only %+v", p)
	}
	for _, sample := range []struct {
		id, level int
		cooldown  time.Duration
	}{{5121010, 5, 1500 * time.Second}, {5821010, 30, 1200 * time.Second}} {
		p := cast(t, sample.id, sample.level)
		if !p.ResetCooldowns || p.Party == nil || len(p.Party.Stats) != 0 || p.Area == nil || p.Cooldown != sample.cooldown ||
			len(p.KeepCooldowns) != 2 || p.KeepCooldowns[0] != 5121010 || p.KeepCooldowns[1] != 5821010 {
			t.Fatalf("time leap %d: %+v", sample.id, p)
		}
	}
	if p := cast(t, 5101005, 10); p.HPCost != 500 || p.Heal == nil || p.Heal.MP != 300 || p.Cooldown != 10*time.Second {
		t.Fatalf("mp recovery %+v %+v", p, p.Heal)
	}
	p := cast(t, 5221006, 10)
	if !p.Toggle || p.Self.Duration != skills.Permanent || p.Self.Stats[0] != (skills.StatValue{Stat: "STANCE", Value: 100}) {
		t.Fatalf("battleship %+v", p.Self)
	}
	if s := stats(p.Self); s["MONSTER_RIDING"] != battleshipMount || s["ENHANCED_WATK"] != 20 || s["ENHANCED_WDEF"] != 700 || s["ENHANCED_MAXHP"] != 600 || s["ENHANCED_MAXMP"] != 600 {
		t.Fatalf("battleship stats %+v", s)
	}
}

func TestPassiveDamageAndStatBonuses(t *testing.T) {
	c := domain.Character{Job: 512}
	if b := passive(t, 5110008, 10, c); b.SkillDamage[0] != (skills.SkillDamage{Skill: 5101002, Percent: 120}) || b.SkillDamage[2] != (skills.SkillDamage{Skill: 5101004, Percent: 150}) {
		t.Fatalf("melee mastery %+v", b.SkillDamage)
	}
	if b := passive(t, 5220001, 30, domain.Character{Job: 522}); b.SkillDamage[1] != (skills.SkillDamage{Skill: 5211005, Percent: 80}) {
		t.Fatalf("elemental boost %+v", b.SkillDamage)
	}
	if b := passive(t, 5300008, 10, domain.Character{Job: 530}); b.STR != 30 || b.DEX != 30 {
		t.Fatalf("monkey training %+v", b)
	}
	if b := passive(t, 5320009, 30, domain.Character{Job: 532}); b.DamagePercent != 40 || b.BossDamagePercent != 40 || b.IgnoreDefense != 20 {
		t.Fatalf("mega monkey cannon %+v", b)
	}
	wantUltimateState := skills.Bonus{MaxHPPercent: 20, ASR: 20, WDEFPercent: 50, MDEFPercent: 50}
	if b := passive(t, 5310007, 20, domain.Character{Job: 531}); b != wantUltimateState {
		t.Fatalf("ultimate state %+v, want %+v", b, wantUltimateState)
	}
	if b := passive(t, 5300004, 15, domain.Character{Job: 530}); b.Critical != 20 || b.CriticalDamageMin != 10 {
		t.Fatalf("critical fire %+v", b)
	}
}

func TestEnergyChargeGauge(t *testing.T) {
	h, _ := skills.Lookup(5110001)
	def, v := values(t, 5110001, 20)
	now := time.Unix(1000, 0)
	attack := &combat.AttackEvent{Targets: []int32{1, 2, 3}, Lines: 2}
	run := func(job int, rows []domain.SkillBuff) *skills.BuffChange {
		return h.OnAttack(&skills.TriggerContext{Skill: def, Level: 20, Values: v, Character: domain.Character{Job: job}, Rows: rows, Attack: attack, Now: now})
	}
	row := func(value int, left time.Duration) []domain.SkillBuff {
		return []domain.SkillBuff{{ID: 5110001, Stat: "ENERGY_CHARGE", Value: value, ExpiresAt: now.Add(left)}}
	}
	if c := run(510, nil); c != nil {
		t.Fatal("brawlers have no energy")
	}
	if c := run(512, nil); c == nil || c.Buff.Duration != skills.Permanent || c.Buff.Stats[0].Value != 0 {
		t.Fatalf("first attack starts the gauge %+v", c)
	}
	if c := run(511, row(0, skills.Permanent)); c.Buff.Stats[0].Value != 1200 || c.Buff.Duration != skills.Permanent {
		t.Fatalf("x=200 per target line %+v", c.Buff)
	}
	if c := run(511, row(9900, skills.Permanent)); c.Buff.Stats[0].Value != energyFull {
		t.Fatalf("capped %+v", c.Buff)
	}
	if c := run(511, row(energyFull, skills.Permanent)); c.Buff.Stats[0].Value != energyFull || c.Buff.Duration != 60*time.Second {
		t.Fatalf("full charge is timed %+v", c.Buff)
	}
	if c := run(511, row(energyFull, 30*time.Second)); c != nil {
		t.Fatalf("timed charge is unchanged %+v", c)
	}
}

func TestEnergyExplosionRequiresFullGaugeAndAddsItsOwnGain(t *testing.T) {
	now := time.Unix(1000, 0)
	ready, _ := skills.Lookup(5121016)
	row := domain.SkillBuff{ID: 5110001, Stat: "ENERGY_CHARGE", Value: 9999, ExpiresAt: now.Add(time.Minute)}
	ctx := &combat.AttackContext{Now: now, Character: domain.Character{Job: 512, SkillBuffs: []domain.SkillBuff{row}}}
	if ready.Attack.Prepare(ctx) == nil {
		t.Fatal("Energy Explosion accepts a partial charge")
	}
	ctx.Character.SkillBuffs[0].Value = energyFull
	if err := ready.Attack.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	def, _ := values(t, 5121016, 30)
	v, _ := def.Values(30)
	ctx.Character.Skills = []domain.SkillState{{ID: 5121016, Level: 30}}
	row.ExpiresAt, row.Value = now.Add(skills.Permanent), 0
	bonus := energyCharge(&skills.TriggerContext{
		Now: now, Values: map[string]int{"x": 200, "time": 60}, Character: ctx.Character,
		Rows: []domain.SkillBuff{row}, Attack: &combat.AttackEvent{Targets: []int32{1}, Lines: 2},
	})
	if bonus == nil || bonus.Buff.Stats[0].Value != 2*(200+v["x"]) || v["x"] != 100 {
		t.Fatalf("Energy Explosion gauge bonus: %+v", bonus)
	}
}

func TestHomingBeaconMarksTheFirstTarget(t *testing.T) {
	for _, id := range []int{5211006, 5220011, 5911006} {
		h, _ := skills.Lookup(id)
		def, v := values(t, id, 10)
		ctx := &skills.TriggerContext{Skill: def, Level: 10, Values: v, Character: domain.Character{Job: 522}, Attack: &combat.AttackEvent{SkillID: id, Targets: []int32{77, 78}}}
		if c := h.OnAttack(ctx); c == nil || c.Buff.Duration != skills.Permanent || c.Buff.Stats[0] != (skills.StatValue{Stat: "HOMING_BEACON", Value: 77}) {
			t.Fatalf("%d %+v", id, c)
		}
		ctx.Attack.SkillID = 5001003
		if c := h.OnAttack(ctx); c != nil {
			t.Fatalf("%d reacts to another skill", id)
		}
	}
}

func TestGaviotaHitsUpToThree(t *testing.T) {
	h, _ := skills.Lookup(5211002)
	ctx := &combat.AttackContext{Effect: combat.Effect{MobCount: 1}}
	if h.Attack.Prepare == nil || h.Attack.Prepare(ctx) != nil || ctx.Effect.MobCount != 3 {
		t.Fatalf("gaviota mob count %d", ctx.Effect.MobCount)
	}
	if h, _ := skills.Lookup(5220002); h.Attack.Prepare != nil {
		t.Fatal("octopus keeps the level mob count")
	}
}

// The client follows Barrel Bomb (5301001) with the invisible blast 5300007,
// which no character learns: the follow-up is cast at Barrel Bomb's level so
// the world does not reject it as unlearned.
func TestBarrelBombFollowUpIsCastAtBarrelBombLevel(t *testing.T) {
	h, ok := skills.Lookup(5300007)
	if !ok || h.Attack == nil || h.LevelFrom != 5301001 || h.Attack.LevelFrom != 5301001 {
		t.Fatalf("5300007 must be linked to 5301001: %+v", h)
	}
	now := time.Unix(0, 0)
	c := domain.Character{Job: 530, Skills: []domain.SkillState{{ID: 5301001, Level: 20}}}
	if got := skills.LinkedLevel(c, h.LevelFrom, 5300007, now); got < 1 {
		t.Fatalf("follow-up level %d, want Barrel Bomb's", got)
	}
	if got := skills.LinkedLevel(domain.Character{Job: 530}, h.LevelFrom, 5300007, now); got != 0 {
		t.Fatalf("unlearned Barrel Bomb still casts the follow-up at %d", got)
	}
	def, err := content.SkillDefinition("", 5300007)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := combat.FromSkill(def, min(20, def.MaxLevel)); err != nil || e.Damage <= 0 || e.MobCount < 2 {
		t.Fatalf("follow-up effect %+v %v", e, err)
	}
}

// Barrel Bomb's barrel rolls x px (350) before bursting; the three hits and
// the hidden blast are admitted that far from the character.
func TestBarrelBombAdmitsTargetsWhereTheBarrelBursts(t *testing.T) {
	for _, id := range []int{5301001, 5300007} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || h.Attack.Prepare == nil {
			t.Fatalf("%d must widen its box", id)
		}
		def, _ := values(t, id, 20)
		e, err := combat.FromSkill(def, 20)
		if err != nil {
			t.Fatal(err)
		}
		before := e.Box
		ctx := &combat.AttackContext{Effect: e}
		if err := h.Attack.Prepare(ctx); err != nil {
			t.Fatal(err)
		}
		if ctx.Effect.Box.LT.X != before.LT.X-350 || ctx.Effect.Box.RB.X != before.RB.X+350 || ctx.Effect.Box.LT.Y != before.LT.Y {
			t.Fatalf("%d box %+v, want %+v widened by 350", id, ctx.Effect.Box, before)
		}
	}
}

// v148 Monkey Super Bomb is also an active ranged attack the client sends as
// 0x56; the Barrel Bomb damR passive stays.
func TestMonkeySuperBombAttacksAndKeepsPassive(t *testing.T) {
	h, ok := skills.Lookup(5311001)
	if !ok || h.Attack == nil || h.Attack.Kind != combat.Shoot || h.Attack.Ammo != combat.AmmoNone || h.Passive == nil {
		t.Fatalf("5311001 handler %+v", h)
	}
}

// Gun Expert's burst shot arrives as its own ranged attack (skill 5220020).
func TestGunExpertBurstShotAttacks(t *testing.T) {
	h, ok := skills.Lookup(5220020)
	if !ok || h.Attack == nil || h.Attack.Kind != combat.Shoot || h.Passive == nil {
		t.Fatalf("5220020 handler %+v", h)
	}
}
