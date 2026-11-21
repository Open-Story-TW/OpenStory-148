package world

import (
	"context"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

const (
	testSummonSkill = 9990002
	testPuppetSkill = 9990003
)

func init() {
	skills.Register(skills.Handler{ID: testSummonSkill, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testSummonSkill, Damage: 100, MobCount: 3, AttackCount: 1},
		Summon: &combat.SummonSpec{MoveAbility: 4, AssistType: 1, Duration: 10 * time.Second}}})
	skills.Register(skills.Handler{ID: testPuppetSkill, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, Effect: combat.Effect{SkillID: testPuppetSkill},
		Summon: &combat.SummonSpec{AssistType: 0, Puppet: true, HP: 50, Duration: 10 * time.Second}}})
}

func summonFixture(t *testing.T) (*Server, []*liveCharacter, []*gameplayTestSink, *gameplayMob, int32) {
	s, lives, sinks, mob := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testSummonSkill, Level: 2}, domain.SkillState{ID: testPuppetSkill, Level: 1})
	oid, err := s.SpawnSummon(1, lives[0], testSummonSkill, 2, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s, lives, sinks, mob, oid
}

func TestSummonSpawnLateJoinMoveAndOwnerLeave(t *testing.T) {
	s, lives, sinks, _, oid := summonFixture(t)
	if sinks[0].count(protocol.SendSpawnSummon) != 1 || sinks[1].count(protocol.SendSpawnSummon) != 1 {
		t.Fatal("spawn not broadcast to owner and peer")
	}
	// Recast replaces the previous same-skill summon.
	if _, err := s.SpawnSummon(1, lives[0], testSummonSkill, 2, time.Now()); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("recast did not replace summon")
	}
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}]
	if len(room.summons) != 1 || room.summons[oid] != nil {
		t.Fatal("old summon survived recast")
	}
	for id := range room.summons {
		oid = id
	}
	s.gameplay.mu.Unlock()
	s.summonMove(1, lives[0], protocol.SummonMoveInput{ObjectID: oid, X: 30, Y: -5, Changed: true, Fragments: []byte{0}})
	s.summonMove(1, lives[1], protocol.SummonMoveInput{ObjectID: oid, X: 999, Changed: true, Fragments: []byte{0}})
	s.gameplay.mu.Lock()
	x := room.summons[oid].x
	s.gameplay.mu.Unlock()
	if x != 30 || sinks[1].count(protocol.SendMoveSummon) != 1 {
		t.Fatalf("summon move x%d (non-owner must not move it)", x)
	}
	s.leaveGameplay(1, 40000, 2, sinks[1])
	late := &gameplayTestSink{}
	if err := s.enterGameplay(1, lives[1], late); err != nil {
		t.Fatal(err)
	}
	if late.count(protocol.SendSpawnSummon) != 1 {
		t.Fatal("late joiner missing summon")
	}
	s.leaveGameplay(1, 40000, 1, sinks[0])
	if late.count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("owner leave did not remove summon")
	}
}

func TestSummonPeerPacketReportsBoundLinesInNativeOrder(t *testing.T) {
	attack := genericAttack{
		summon:           &roomSummon{ownerID: 1, objectID: 900},
		summonOwnerLevel: 100,
		summonAction:     132,
		summonHitOrder:   []int{0, 1, 0, 1},
		attack: protocol.Attack{Targets: []protocol.AttackTarget{
			{ObjectID: 1001, Lines: []protocol.AttackLine{{Damage: 5}, {Damage: 7}}},
			{ObjectID: 1002, Lines: []protocol.AttackLine{{Damage: 4}, {Damage: 3}}},
		}},
	}
	r := protocol.NewReader(attack.remotePacket(domain.Character{}, nil))
	if r.U16() != protocol.SendSummonAttack || r.I32() != 1 || r.I32() != 900 || r.U8() != 99 || r.U8() != 132 || r.U8() != 4 {
		t.Fatalf("incorrect native summon attack header: %+v", attack)
	}
	for _, want := range [][2]int{{1001, 5}, {1002, 4}, {1001, 7}, {1002, 3}} {
		if oid, anim, damage := r.I32(), r.U8(), r.U32(); int(oid) != want[0] || anim != 7 || int(damage) != want[1] {
			t.Fatalf("peer row got %d %d %d want %v", oid, anim, damage, want)
		}
	}
	if r.U8() != 0 || r.Remaining() != 0 || r.Err() != nil {
		t.Fatal("malformed summon peer tail")
	}
}

func TestSingleHitSummonDoesNotGainExtraDamageFromRepeatedWireObject(t *testing.T) {
	s, lives, sinks, mob, oid := summonFixture(t)
	mob.hp = 1000
	in := protocol.SummonAttackInput{ObjectID: oid, Tick: 5, Targets: []protocol.AttackTarget{{
		ObjectID: mob.objectID,
		Lines:    []protocol.AttackLine{{Raw: 7, Damage: 7}, {Raw: 7, Damage: 7}},
	}}, HitOrder: []int{0, 0}}
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	if mob.hp != 1000 || sinks[1].count(protocol.SendSummonAttack) != 0 {
		t.Fatalf("single-hit summon admitted repeated damage: HP %d", mob.hp)
	}
}

func TestSummonAttackSettlesWithoutCostAndExpires(t *testing.T) {
	s, lives, sinks, mob, oid := summonFixture(t)
	mob.hp = 1000
	in := protocol.SummonAttackInput{ObjectID: oid, Tick: 5, Action: 0x80 | 3, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 7, Damage: 7}}}}}
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return fakeSharedResult(q), nil
	}}
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	s.gameplay.mu.Lock()
	hp := mob.hp
	s.gameplay.mu.Unlock()
	if hp != 993 || calls != 0 || sinks[1].count(protocol.SendSummonAttack) != 1 || sinks[0].count(protocol.SendSummonAttack) != 0 {
		t.Fatalf("nonlethal summon attack hp%d calls%d", hp, calls)
	}
	// A peer cannot drive another player's summon.
	if err := s.summonAttackGameplay(context.Background(), 1, lives[1], sinks[1], protocol.SummonAttackInput{ObjectID: oid, Tick: 9, Targets: in.Targets}); err != nil {
		t.Fatal(err)
	}
	if sinks[0].count(protocol.SendSummonAttack) != 0 {
		t.Fatal("foreign summon control accepted")
	}
	// Lethal attack goes through settlement with a zero-cost summon request.
	var got domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		got = q
		return fakeSharedResult(q), nil
	}}
	s.gameplay.mu.Lock()
	mob.hp = 5
	s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].summons[oid].lastAttack = time.Now().Add(-time.Second) // past the 200ms throttle
	s.gameplay.mu.Unlock()
	in.Tick = 77
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	if got.Cost == nil || got.Cost.Summon != oid || got.Cost.MP != 0 || len(got.Targets) != 1 || !got.Targets[0].Defeated || got.Targets[0].Damage != 5 {
		t.Fatalf("summon settlement %+v", got)
	}
	s.gameplay.tickSummons(time.Now().Add(11 * time.Second))
	if sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("summon did not expire")
	}
}

func TestAF11ExpiryAttackUsesOriginalActionBoundsAndObjectOnce(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	s, lives, sinks, mob := sharedWorldFixture(t)
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	c := &lives[0].session.Character
	c.Job, c.Equipment[0].ItemID = 3512, 1492004
	c.Skills = append(c.Skills, domain.SkillState{ID: 35121010, Level: 20})
	mob.hp = 1000
	stamp := time.Now()
	oid, err := s.SpawnSummon(1, lives[0], 35121010, 20, stamp)
	if err != nil {
		t.Fatal(err)
	}
	room := s.gameplay.rooms[mapKey{channel: 1, mapID: c.MapID}]
	m := room.summons[oid]
	if m.expiresAt != stamp.Add(80*time.Second) || m.deathWindow != 1080*time.Millisecond || m.deathEffect.Damage != 300 || m.deathEffect.MobCount != 6 || m.deathEffect.AttackCount != 1 || m.deathEffect.Box.LT != (combat.Point{X: -120, Y: -120}) || m.deathEffect.Box.RB != (combat.Point{X: 120, Y: 0}) {
		t.Fatalf("AF-11 v148 data: duration %s, death window %s, effect %+v", m.expiresAt.Sub(stamp), m.deathWindow, m.deathEffect)
	}
	mob.life.X, mob.life.Y = m.x, m.y
	in := protocol.SummonAttackInput{ObjectID: oid, Tick: 5, Action: 0x11, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 7, Damage: 7}}}}}
	attack := func() {
		t.Helper()
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
			t.Fatal(err)
		}
	}
	attack() // The real terminal mode cannot damage targets while AF-11 is active.
	m.expiresAt = time.Now().Add(-25 * time.Millisecond)
	s.gameplay.tickSummons(time.Now())
	if room.summons[oid] != m {
		t.Fatal("world discarded the original object during its death animation")
	}
	if m.amplifierAreaActive || sinks[0].count(protocol.SendAffectedAreaRemoved) != 1 || sinks[1].count(protocol.SendAffectedAreaRemoved) != 1 {
		t.Fatal("expiry did not remove the area independently of the death animation")
	}
	in.Action = 4
	attack() // The normal summon action is invalid even inside the death window.
	in.Action = 0x11
	mob.life.X = m.x + 121
	in.X = int16(mob.life.X)
	attack() // The client cannot move the origin of the immobile robot's explosion.
	if mob.hp != 1000 {
		t.Fatalf("early, wrong-action or out-of-bounds AF-11 attack damaged mob: %d", mob.hp)
	}
	mob.life.X = m.x + 96
	attack()
	in.Tick++
	attack() // A different tick cannot replay the terminal attack.
	if mob.hp != 993 || sinks[1].count(protocol.SendSummonAttack) != 1 {
		t.Fatalf("AF-11 terminal attack did not settle exactly once: hp %d", mob.hp)
	}
	s.gameplay.tickSummons(time.Now().Add(time.Second))
	if room.summons[oid] != nil || sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("AF-11 was not removed after its terminal attack")
	}
}

func TestBotsNTotsMiniBotExpirySelfDestruction(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	for _, action := range []byte{0x11, 0x91} {
		s, lives, sinks, mob := sharedWorldFixture(t)
		s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
		c := &lives[0].session.Character
		c.Job, c.Equipment[0].ItemID = 3512, 1492004
		c.Skills = append(c.Skills, domain.SkillState{ID: 35121009, Level: 30})
		mob.hp = 1000
		stamp := time.Now()
		oid, err := s.SpawnSummon(1, lives[0], 35121011, 30, stamp)
		if err != nil {
			t.Fatalf("action 0x%x: SpawnSummon(35121011): %v", action, err)
		}
		room := s.gameplay.rooms[mapKey{channel: 1, mapID: c.MapID}]
		m := room.summons[oid]
		if m.expiresAt != stamp.Add(5*time.Second) || m.deathWindow != 720*time.Millisecond || m.deathEffect.Damage != 350 || m.deathEffect.MobCount != 4 || m.deathEffect.AttackCount != 1 {
			t.Fatalf("action 0x%x: 35121011 v148 data: duration %s, deathWindow %s, effect %+v", action, m.expiresAt.Sub(stamp), m.deathWindow, m.deathEffect)
		}
		// Mini-bot walks 40px right and hits a mob 95px away (within 2x rangeSlack of +-80 box).
		botX := m.x + 40
		mob.life.X, mob.life.Y = botX-95, m.y
		in := protocol.SummonAttackInput{ObjectID: oid, Tick: 9, Action: action, X: int16(botX), Y: int16(m.y),
			Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 12, Damage: 12}}}}}
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
			t.Fatal(err)
		}
		if mob.hp != 1000 {
			t.Fatalf("action 0x%x: early 35121011 attack should be ignored", action)
		}
		// Enter expiry death window (50ms past 5s expiry, matching live 5.02-5.07s wire captures).
		m.expiresAt = time.Now().Add(-50 * time.Millisecond)
		s.gameplay.tickSummons(time.Now())
		if room.summons[oid] != m {
			t.Fatalf("action 0x%x: world discarded 35121011 during its death window", action)
		}
		bad := in
		bad.Action = 0x04
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], bad); err != nil {
			t.Fatal(err)
		}
		if mob.hp != 1000 {
			t.Fatalf("action 0x%x: non-expiry action 0x04 should be rejected", action)
		}
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
			t.Fatal(err)
		}
		if mob.hp != 988 || sinks[1].count(protocol.SendSummonAttack) != 1 || room.summons[oid] != nil {
			t.Fatalf("action 0x%x: 35121011 self-destruction failed: hp=%d peer0x1a4=%d stillInRoom=%v", action, mob.hp, sinks[1].count(protocol.SendSummonAttack), room.summons[oid] != nil)
		}
	}
}

func TestWildHunterMineExpirySelfDestruction(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	for _, action := range []byte{0x11, 0x91} {
		s, lives, sinks, mob := sharedWorldFixture(t)
		s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
		c := &lives[0].session.Character
		c.Job, c.Equipment[0].ItemID = 3310, 1462000
		c.Skills = append(c.Skills, domain.SkillState{ID: 33101004, Level: 20})
		mob.hp = 1000
		stamp := time.Now()
		oid, err := s.SpawnSummon(1, lives[0], 33101008, 20, stamp)
		if err != nil {
			t.Fatalf("action 0x%x: SpawnSummon(33101008): %v", action, err)
		}
		room := s.gameplay.rooms[mapKey{channel: 1, mapID: c.MapID}]
		m := room.summons[oid]
		if m.expiresAt != stamp.Add(2*time.Second) || m.deathWindow != 630*time.Millisecond || m.deathEffect.Damage != 280 || m.deathEffect.MobCount != 3 || m.deathEffect.AttackCount != 1 {
			t.Fatalf("action 0x%x: 33101008 v148 data: duration %s, deathWindow %s, effect %+v", action, m.expiresAt.Sub(stamp), m.deathWindow, m.deathEffect)
		}
		mob.life.X, mob.life.Y = m.x+30, m.y
		in := protocol.SummonAttackInput{ObjectID: oid, Tick: 11, Action: action, X: int16(m.x), Y: int16(m.y),
			Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 15, Damage: 15}}}}}
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
			t.Fatal(err)
		}
		if mob.hp != 1000 {
			t.Fatalf("action 0x%x: early 33101008 attack should be ignored", action)
		}
		m.expiresAt = time.Now().Add(-50 * time.Millisecond)
		s.gameplay.tickSummons(time.Now())
		if room.summons[oid] != m {
			t.Fatalf("action 0x%x: world discarded 33101008 during its death window", action)
		}
		bad := in
		bad.Action = 0x04
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], bad); err != nil {
			t.Fatal(err)
		}
		if mob.hp != 1000 {
			t.Fatalf("action 0x%x: non-expiry action 0x04 should be rejected", action)
		}
		if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
			t.Fatal(err)
		}
		if mob.hp != 985 || sinks[1].count(protocol.SendSummonAttack) != 1 || room.summons[oid] != nil {
			t.Fatalf("action 0x%x: 33101008 self-destruction failed: hp=%d peer0x1a4=%d stillInRoom=%v", action, mob.hp, sinks[1].count(protocol.SendSummonAttack), room.summons[oid] != nil)
		}
	}
}

func TestAF11AffectedAreaAdmitsOnlyTheExactOwnerOrAnInRangePartyMember(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	s, lives, sinks, _ := sharedWorldFixture(t)
	now := time.Now()
	caster := &lives[0].session.Character
	caster.Job, caster.X, caster.Y = 3512, 50, 215
	caster.Skills = append(caster.Skills, domain.SkillState{ID: af11Skill, Level: 20})
	pos := combat.Point{X: caster.X, Y: caster.Y}
	oid, err := s.spawnSummonAt(1, lives[0], af11Skill, 20, &pos, now, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	room := s.gameplay.rooms[mapKey{channel: 1, mapID: caster.MapID}]
	for _, sink := range sinks {
		if sink.count(protocol.SendSpawnSummon) != 1 || sink.count(protocol.SendAffectedAreaCreated) != 1 {
			t.Fatal("owner and observer must both receive the summon and its amplification area")
		}
	}
	m := room.summons[oid]
	r := protocol.NewReader(m.amplifierAreaPacket(now))
	if r.U16() != protocol.SendAffectedAreaCreated || r.I32() != oid || r.I32() != 1 || r.I32() != int32(caster.ID) ||
		r.I32() != af11Skill || r.U8() != 20 || r.U16() != 0 || r.I32() != -150 || r.I32() != 125 || r.I32() != 250 || r.I32() != 240 {
		t.Fatal("AF-11 area packet does not encode the exact original WZ rectangle and owner")
	}
	if got := summonEnterPacketsLocked(room); len(got) != 2 {
		t.Fatal("late entrant did not receive both live objects")
	}
	party := domain.PartySnapshot{ID: 1, Members: []domain.PartyMember{{CharacterID: caster.ID}, {CharacterID: lives[1].session.Character.ID}}}
	member := lives[1].session.Character
	member.X, member.Y = 249, 239
	if physical, magic := amplifierDamageLocked(room, member, party, now); physical != 20 || magic != 20 {
		t.Fatalf("in-range member got %d physical and %d magic", physical, magic)
	}
	if physical, _ := amplifierDamageLocked(room, member, domain.PartySnapshot{}, now); physical != 0 {
		t.Fatal("amplification leaked to an outsider")
	}
	if physical, _ := amplifierDamageLocked(room, *caster, domain.PartySnapshot{}, now); physical != 20 {
		t.Fatal("unpartied owner did not receive own amplification")
	}
	for _, outside := range []combat.Point{{X: 250, Y: 215}, {X: 50, Y: 240}, {X: -151, Y: 215}} {
		member.X, member.Y = outside.X, outside.Y
		if physical, magic := amplifierDamageLocked(room, member, party, now); physical != 0 || magic != 0 {
			t.Fatalf("member outside the native rectangle at %+v got %d/%d", outside, physical, magic)
		}
	}
	s.leaveGameplay(1, caster.MapID, caster.ID, sinks[0])
	if sinks[1].count(protocol.SendAffectedAreaRemoved) != 1 || sinks[1].count(protocol.SendRemoveSummon) != 1 || len(room.summons) != 0 {
		t.Fatal("AF-11 left a detached area or robot after its owner left")
	}
}

func TestSG88BodyRequiresItsLiveOwnedRobot(t *testing.T) {
	const id = 35121003
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	s, lives, sinks, mob := sharedWorldFixture(t)
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return fakeSharedResult(q), nil
	}}
	now := time.Now()
	for _, live := range lives {
		c := &live.session.Character
		c.Job, c.Equipment[0].ItemID = 3512, 1492004
		c.Skills = append(c.Skills, domain.SkillState{ID: id, Level: 30})
		c.SkillBuffs = append(c.SkillBuffs, domain.SkillBuff{ID: id, Stat: "COMBO", Value: 1, ExpiresAt: now.Add(time.Minute)})
	}
	mob.hp, mob.life.X, mob.life.Y = 1000, 0, 0
	attack := func(owner int, tick uint32) {
		t.Helper()
		a := genericMelee(id, tick, mob.objectID, 7)
		a.Opcode = protocol.RecvBodyAttack
		if err := s.genericAttackGameplay(context.Background(), 1, lives[owner], sinks[owner], a); err != nil {
			t.Fatal(err)
		}
	}
	attack(0, 1)
	if _, err := s.SpawnSummon(1, lives[0], id, 30, now); err != nil {
		t.Fatal(err)
	}
	attack(1, 2)
	if mob.hp != 1000 {
		t.Fatalf("SG-88 pulse accepted with no live owned robot: hp %d", mob.hp)
	}
	attack(0, 3)
	if mob.hp != 993 || calls != 0 || sinks[1].count(protocol.SendBodyAttack) != 1 {
		t.Fatalf("SG-88 pulse did not settle once as a free body attack: hp %d, calls %d", mob.hp, calls)
	}
}

func TestSG88SummonUsesOriginalChargeWithoutExceedingDoubleDamage(t *testing.T) {
	const id = 35121003
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	template, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		gauge         uint32
		damagePercent int
	}{
		{"uncharged", 0, 1440},
		{"original forty", 40, 2016},
		{"full", 100, 2880},
		{"outside original scale", ^uint32(0), 2880},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			s.repo = &sharedCombatRepo{}
			c := &lives[0].session.Character
			c.Job, c.Equipment[0].ItemID = 3512, 1492004
			c.Skills = append(c.Skills, domain.SkillState{ID: id, Level: 30})
			now := time.Now()
			oid, err := s.SpawnSummon(1, lives[0], id, 30, now)
			if err != nil {
				t.Fatal(err)
			}
			stats, err := combat.AttackStats(*c, now)
			if err != nil {
				t.Fatal(err)
			}
			effect, err := combat.SummonEffect(template, 30)
			if err != nil {
				t.Fatal(err)
			}
			effect.Damage = tc.damagePercent
			mob.hp = 1_000_000
			want := combat.SummonCeiling(combat.DamageContext{Stats: stats, Effect: effect, Mob: mob.stats, HP: mob.hp, Attacker: *c})
			const claim = 500_000
			in := protocol.SummonAttackInput{ObjectID: oid, Tick: 3, Action: 132, ChargeGauge: tc.gauge,
				Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: claim, Damage: claim}}}}}
			if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
				t.Fatal(err)
			}
			if got := 1_000_000 - mob.hp; got != int64(want) || want <= 0 || sinks[1].count(protocol.SendSummonAttack) != 1 {
				t.Fatalf("SG-88 settled %d damage, want %d with charge %d", got, want, tc.gauge)
			}
		})
	}
}

func TestPuppetAbsorbsDamageAndDies(t *testing.T) {
	s, lives, sinks, _, _ := summonFixture(t)
	oid, err := s.SpawnSummon(1, lives[0], testPuppetSkill, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.summonDamaged(1, lives[0], protocol.SummonDamageInput{ObjectID: oid, AttackIndex: -1, Damage: 30})
	s.summonDamaged(1, lives[0], protocol.SummonDamageInput{ObjectID: oid, AttackIndex: 0, Damage: 30, MobTemplate: 100100})
	if sinks[1].count(protocol.SendDamageSummon) != 2 || sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("puppet damage/death")
	}
}

const testBeholder = 9990008

func init() {
	skills.Register(skills.Handler{ID: testBeholder, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Effect: combat.Effect{SkillID: testBeholder},
		Summon: &combat.SummonSpec{MoveAbility: 1, AssistType: 2, Duration: time.Minute, Sub: func(c combat.SubSummonContext) (combat.SubSummonResult, error) {
			if c.SubSkill != 1001004 {
				return combat.SubSummonResult{}, combat.ErrRejected
			}
			return combat.SubSummonResult{HealHP: 25, Action: 5}, nil
		}}}})
}

func TestBeholderSubSkillHealsOwnerAndBroadcasts(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testBeholder, Level: 1}, domain.SkillState{ID: 1001004, Level: 1})
	oid, err := s.SpawnSummon(1, lives[0], testBeholder, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.summonSub(1, lives[0], sinks[0], protocol.SubSummonInput{ObjectID: oid, SkillID: 1001004}); err != nil {
		t.Fatal(err)
	}
	if lives[0].session.Character.HP != 75 || sinks[1].count(protocol.SendSummonSkill) != 1 {
		t.Fatalf("beholder heal hp%d", lives[0].session.Character.HP)
	}
	// Peers cannot trigger another player's summon skill.
	_ = s.summonSub(1, lives[1], sinks[1], protocol.SubSummonInput{ObjectID: oid, SkillID: 1001004})
	if sinks[0].count(protocol.SendSummonSkill) != 1 {
		t.Fatal("foreign sub-summon accepted")
	}
}

func TestMalformedSummonPacketsDoNotPoisonTheConnectionReader(t *testing.T) {
	s, lives, sinks, _, _ := summonFixture(t)
	for _, op := range []uint16{protocol.RecvMoveSummon, protocol.RecvSummonAttack, protocol.RecvDamageSummon, protocol.RecvSubSummon, protocol.RecvRemoveSummon} {
		r := protocol.NewReader([]byte{1, 2, 3})
		handled, err := s.handleGameplay(context.Background(), 1, lives[0], sinks[0], op, r)
		if !handled || err != nil || r.Err() != nil {
			t.Fatalf("op 0x%x: handled=%v err=%v readerErr=%v", op, handled, err, r.Err())
		}
	}
}

const (
	testFactory    = 9990013
	testFactoryBot = 9990014
	testMine       = 9990015
)

func init() {
	skills.Register(skills.Handler{ID: testFactoryBot, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, Effect: combat.Effect{SkillID: testFactoryBot},
		Summon: &combat.SummonSpec{MoveAbility: 2, AssistType: 1, Multi: true, Duration: 5 * time.Second}}})
	skills.Register(skills.Handler{ID: testFactory, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Effect: combat.Effect{SkillID: testFactory},
		Summon: &combat.SummonSpec{AssistType: 5, Duration: time.Minute, Sub: func(combat.SubSummonContext) (combat.SubSummonResult, error) {
			return combat.SubSummonResult{Spawn: []combat.SummonChild{{SkillID: testFactoryBot, Level: 1}, {SkillID: testFactoryBot, Level: 1}, {SkillID: testFactoryBot, Level: 1}}}, nil
		}}}})
	skills.Register(skills.Handler{ID: testMine, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true, Effect: combat.Effect{SkillID: testMine, Damage: 100, MobCount: 3, AttackCount: 1},
		Summon: &combat.SummonSpec{AssistType: 0, SingleAttack: true, Multi: true, Duration: 2 * time.Second}}})
}

func TestFactorySubSpawnsChildrenAndMinesAttackOnce(t *testing.T) {
	s, lives, sinks, mob, _ := summonFixture(t)
	mob.hp = 1000
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testFactory, Level: 1}, domain.SkillState{ID: testFactoryBot, Level: 1}, domain.SkillState{ID: testMine, Level: 1})
	fid, err := s.SpawnSummon(1, lives[0], testFactory, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before := sinks[1].count(protocol.SendSpawnSummon)
	if err = s.summonSub(1, lives[0], sinks[0], protocol.SubSummonInput{ObjectID: fid}); err != nil {
		t.Fatal(err)
	}
	if sinks[1].count(protocol.SendSpawnSummon) != before+3 {
		t.Fatal("factory did not spawn three robots")
	}
	mid, err := s.SpawnSummon(1, lives[0], testMine, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	in := protocol.SummonAttackInput{ObjectID: mid, Tick: 3, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 9, Damage: 9}}}}}
	if err = s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	s.gameplay.mu.Lock()
	_, still := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].summons[mid]
	s.gameplay.mu.Unlock()
	if mob.hp != 991 || still {
		t.Fatalf("mine attack hp%d still=%v", mob.hp, still)
	}
}

func TestFreshSummonAdmitsImmediateFirstSubSummon(t *testing.T) {
	s, lives, sinks, _, _ := summonFixture(t)
	var firstLastSub time.Time
	var calls int
	const testFirstSub = 9990041
	skills.Register(skills.Handler{ID: testFirstSub, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Effect: combat.Effect{SkillID: testFirstSub},
		Summon: &combat.SummonSpec{AssistType: 2, Duration: time.Minute, Sub: func(c combat.SubSummonContext) (combat.SubSummonResult, error) {
			calls++
			if calls == 1 {
				firstLastSub = c.LastSub
			}
			if !c.LastSub.IsZero() && c.Now.Before(c.LastSub.Add(10*time.Minute)) {
				return combat.SubSummonResult{}, errors.New("sub not ready")
			}
			return combat.SubSummonResult{Action: 8}, nil
		}}}})
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testFirstSub, Level: 1})
	oid, err := s.SpawnSummon(1, lives[0], testFirstSub, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before := sinks[0].count(protocol.SendSummonSkill)
	if err = s.summonSub(1, lives[0], sinks[0], protocol.SubSummonInput{ObjectID: oid, SkillID: testFirstSub}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !firstLastSub.IsZero() || sinks[0].count(protocol.SendSummonSkill) != before+1 {
		t.Fatalf("first sub rejected on fresh summon: calls=%d firstLastSub=%v skillPkts=%d", calls, firstLastSub, sinks[0].count(protocol.SendSummonSkill)-before)
	}
	// Immediate second sub inside the 10-minute interval must be rejected.
	if err = s.summonSub(1, lives[0], sinks[0], protocol.SubSummonInput{ObjectID: oid, SkillID: testFirstSub}); err != nil {
		t.Fatal(err)
	}
	if sinks[0].count(protocol.SendSummonSkill) != before+1 {
		t.Fatal("second sub inside interval was not throttled")
	}
}

const (
	testKnights    = 9990016
	testKnightsAlt = 9990017
	testRemapFrom  = 9990018
	testRemapTo    = 9990019
	testNoCast     = 9990020
	testBlink      = 9990021
	testTwins      = 9990031
	testTwin       = 9990032
)

var seenSummonSkill int

func init() {
	skills.Register(skills.Handler{ID: testKnights, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true, Prepare: func(c *combat.AttackContext) error { seenSummonSkill = c.SummonSkillID; return nil },
		Effect: combat.Effect{SkillID: testKnights, Damage: 100, MobCount: 1, AttackCount: 1},
		Summon: &combat.SummonSpec{AssistType: 1, Duration: time.Minute, Variants: []int{testKnightsAlt}}}})
	skills.Register(skills.Handler{ID: testNoCast, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, Effect: combat.Effect{SkillID: testNoCast, Damage: 100, MobCount: 1, AttackCount: 1},
		Summon: &combat.SummonSpec{AssistType: 1, Duration: time.Minute, NoCastSpawn: true}}})
	skills.Register(skills.Handler{ID: testBlink, Kind: skills.KindBuff, Attack: &combat.AttackSpec{TeleportKey: true}})
	skills.Register(skills.Handler{ID: testTwins, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testTwins, Damage: 100, MobCount: 1, AttackCount: 1},
		Summon: &combat.SummonSpec{AssistType: 1, Duration: time.Minute, Twins: []int{testTwin}}}})
	skills.Register(skills.Handler{ID: testRemapTo, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true, NoCooldown: true, Effect: combat.Effect{SkillID: testRemapTo, Damage: 100, MobCount: 1, AttackCount: 1, Cooltime: time.Minute}}})
	skills.Register(skills.Handler{ID: testRemapFrom, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true,
		Effect: combat.Effect{SkillID: testRemapFrom, Damage: 100, MobCount: 1, AttackCount: 1},
		Remap:  func(domain.Character, time.Time) int { return testRemapTo }}})
}

func TestSummonVariantsAndAttackRemap(t *testing.T) {
	s, lives, sinks, mob, _ := summonFixture(t)
	mob.hp = 1000
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testKnights, Level: 1}, domain.SkillState{ID: testRemapFrom, Level: 1}, domain.SkillState{ID: testRemapTo, Level: 1})
	oid, err := s.SpawnSummon(1, lives[0], testKnights, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s.gameplay.mu.Lock()
	shown := s.gameplay.rooms[mapKey{channel: 1, mapID: 40000}].summons[oid].skillID
	s.gameplay.mu.Unlock()
	if shown != testKnightsAlt {
		t.Fatalf("variant not shown: %d", shown)
	}
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	in := protocol.SummonAttackInput{ObjectID: oid, Tick: 4, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 3, Damage: 3}}}}}
	if err = s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil || mob.hp != 997 {
		t.Fatalf("variant summon attack hp%d %v", mob.hp, err)
	}
	if seenSummonSkill != testKnightsAlt {
		t.Fatalf("attack context summon skill %d", seenSummonSkill)
	}
	s.castCombatObjects(lives[0].session, lives[0], sinks[0], protocol.SkillCast{SkillID: testNoCast, Level: 1})
	if n := s.summonCount(mapKey{channel: 1, mapID: 40000}, lives[0].session.CharacterID, testNoCast); n != 0 {
		t.Fatalf("NoCastSpawn cast spawned a summon: %d", n)
	}
	s.castCombatObjects(lives[0].session, lives[0], sinks[0], protocol.SkillCast{SkillID: testBlink, Level: 1})
	if sinks[0].count(protocol.SendRandomTeleportKey) != 1 {
		t.Fatal("Dragon Blink cast did not re-roll the teleport key")
	}
	var got domain.CombatAttackRequest
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		got = q
		return fakeSharedResult(q), nil
	}}
	mob.hp = 2
	if err = s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], genericMelee(testRemapFrom, 41, mob.objectID, 5)); err != nil {
		t.Fatal(err)
	}
	if got.SkillID != testRemapTo || got.Cost == nil || got.Cost.CooldownMS != 0 {
		t.Fatalf("remap/no-cooldown %+v", got)
	}
}

func TestSummonTwinsSpawnAttackAndLeaveTogether(t *testing.T) {
	s, lives, sinks, mob, _ := summonFixture(t)
	mob.hp = 1000
	lives[0].session.Character.Skills = append(lives[0].session.Character.Skills, domain.SkillState{ID: testTwins, Level: 1})
	key := mapKey{channel: 1, mapID: 40000}
	for range 2 { // a recast replaces both
		if _, err := s.SpawnSummon(1, lives[0], testTwins, 1, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var twin int32
	shown := map[int]int{}
	s.gameplay.mu.Lock()
	for oid, m := range s.gameplay.rooms[key].summons {
		if m.source == testTwins {
			shown[m.skillID]++
			if m.skillID == testTwin {
				twin = oid
			}
		}
	}
	s.gameplay.mu.Unlock()
	if shown[testTwins] != 1 || shown[testTwin] != 1 {
		t.Fatalf("summons shown %v", shown)
	}
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) { return fakeSharedResult(q), nil }}
	in := protocol.SummonAttackInput{ObjectID: twin, Tick: 4, Targets: []protocol.AttackTarget{{ObjectID: mob.objectID, Lines: []protocol.AttackLine{{Raw: 3, Damage: 3}}}}}
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil || mob.hp != 997 {
		t.Fatalf("twin attack hp%d %v", mob.hp, err)
	}
	s.RemoveSummons(1, 40000, lives[0].session.CharacterID, testTwins)
	if n := s.summonCount(key, lives[0].session.CharacterID, testTwins); n != 0 {
		t.Fatalf("%d summons left after the buff ended", n)
	}
}
