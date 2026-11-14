package world

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const eruptionNativeFull = "570002152b5d20000000bd8a67002601f00d79a906069b08921e000000007d420f00078003017fffd70082ffd70038045d0a00006b080000a40300009a0700005d0a0000db323b5856b5031115ffd70000017e420f00"

func eruptionFixture(t *testing.T) (*Server, []*liveCharacter, []*gameplayTestSink, *gameplayMob, int32, protocol.Attack) {
	t.Helper()
	s, lives, sinks, mob := sharedWorldFixture(t)
	c := &lives[0].session.Character
	c.Job, c.Level, c.INT, c.MP, c.MaxMP = 212, 120, 1000, 500, 500
	c.X, c.Y = -235, 215
	c.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1382000, MATK: 200}}
	c.Skills = []domain.SkillState{{ID: 2111003, Level: 20}, {ID: 2121003, Level: 30}}
	oid, err := s.SpawnFieldObject(1, lives[0], 2111003, 20, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := hex.DecodeString(eruptionNativeFull)
	wire = append(wire[:2:2], protocol.UpgradeV145AttackBody(protocol.RecvMagicAttack, wire[2:])...) // archived v145 frame
	a, err := protocol.ReadAttack(protocol.RecvMagicAttack, protocol.NewReader(wire[2:]))
	if err != nil {
		t.Fatal(err)
	}
	a.EruptionAreas[0] = oid
	a.Targets[0].ObjectID = mob.objectID
	// Keep the fixture body bound to its adapted server object identities.
	binary.LittleEndian.PutUint32(a.Wire[len(a.Wire)-4:], uint32(oid))
	binary.LittleEndian.PutUint32(a.Wire[28:32], uint32(mob.objectID))
	mob.hp = 55000
	mob.life.X, mob.life.Y = c.X, c.Y
	return s, lives, sinks, mob, oid, a
}

func eruptionResult(q domain.CombatAttackRequest) domain.CombatAttackResult {
	r := fakeSharedResult(q)
	for i := range r.Characters {
		if r.Characters[i].ID == q.Session.CharacterID {
			r.Characters[i].MP -= q.Cost.MP
			r.Characters[i].SkillCooldowns = append(r.Characters[i].SkillCooldowns, domain.SkillCooldown{ID: 2121003, ExpiresAt: time.Now().Add(time.Duration(q.Cost.CooldownMS) * time.Millisecond)})
		}
	}
	return r
}

func TestMistEruptionActualFiveLinesAndOnlySelectedArea(t *testing.T) {
	s, lives, sinks, mob, oid, a := eruptionFixture(t)
	other, err := s.SpawnFieldObject(1, lives[0], 2111003, 20, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		if q.SkillID != 2121003 || q.Cost.MP != 100 || q.Cost.CooldownMS != 10000 || q.LinesPerTarget != 5 || len(q.Targets) != 1 || len(q.Targets[0].Lines) != 5 || q.Targets[0].Damage != 10339 || len(q.Wire) != 88 || binary.LittleEndian.Uint32(q.Wire[84:88]) != uint32(oid) {
			t.Fatalf("actual eruption settlement: %+v", q)
		}
		return eruptionResult(q), nil
	}}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	room := s.gameplay.rooms[mapKey{1, 40000}]
	if calls != 1 || mob.hp != 44661 || lives[0].session.Character.MP != 400 || room.areas[oid] != nil || room.areas[other] == nil || sinks[1].count(protocol.SendAffectedAreaRemoved) != 1 || sinks[1].count(protocol.SendMagicAttack) != 1 {
		t.Fatalf("effect: calls%d hp%d mp%d areas%v", calls, mob.hp, lives[0].session.Character.MP, room.areas)
	}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || mob.hp != 44661 {
		t.Fatal("replay repeated damage/cost")
	}
}

func TestMistEruptionRejectsInvalidAreaWithoutDamageOrCost(t *testing.T) {
	for _, kind := range []string{"missing", "foreign", "generation", "visit", "expired", "reserved", "wrong_skill", "duplicate", "empty", "over_limit", "distant", "right_edge", "bottom_edge"} {
		t.Run(kind, func(t *testing.T) {
			s, lives, sinks, mob, oid, a := eruptionFixture(t)
			room := s.gameplay.rooms[mapKey{1, 40000}]
			area := room.areas[oid]
			switch kind {
			case "missing":
				delete(room.areas, oid)
			case "foreign":
				area.ownerID = 2
			case "generation":
				area.ownerGeneration++
			case "visit":
				copy := *room.members[1]
				room.members[1] = &copy
			case "expired":
				area.expiresAt = time.Now().Add(-time.Second)
			case "reserved":
				area.reservation = &combatTicket{}
			case "wrong_skill":
				area.skillID = 2111004
			case "duplicate":
				a.EruptionAreas = append(a.EruptionAreas, oid)
			case "empty":
				a.EruptionAreas = nil
			case "over_limit":
				a.EruptionAreas = []int32{oid, 1, 2, 3, 4, 5, 6}
			case "distant":
				area.x = lives[0].session.Character.X + 501
			case "right_edge":
				area.x = lives[0].session.Character.X + 500
			case "bottom_edge":
				area.y = lives[0].session.Character.Y + 350
			}
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return eruptionResult(q), nil
			}}
			if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
				t.Fatal(err)
			}
			if calls != 0 || mob.hp != 55000 || lives[0].session.Character.MP != 500 || sinks[1].count(protocol.SendAffectedAreaRemoved) != 0 || sinks[1].count(protocol.SendMagicAttack) != 0 {
				t.Fatal("invalid area mutated combat")
			}
		})
	}
}

func TestMistEruptionUnknownOutcomeRetainsExactAreaUntilResolve(t *testing.T) {
	s, lives, sinks, mob, oid, a := eruptionFixture(t)
	calls := 0
	var result domain.CombatAttackResult
	resolved := false
	repo := &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		result = eruptionResult(q)
		return domain.CombatAttackResult{}, errors.New("lost after commit")
	}, outcome: func(string, string) (*domain.CombatAttackResult, error) {
		if !resolved {
			return nil, errors.New("receipt unavailable")
		}
		return &result, nil
	}}
	s.repo = repo
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	room := s.gameplay.rooms[mapKey{1, 40000}]
	area := room.areas[oid]
	if area == nil || area.reservation == nil || mob.hp != 55000 || calls != 1 {
		t.Fatal("unknown changed area/HP or failed to reserve")
	}
	s.gameplay.tickAreas(time.Now().Add(time.Hour))
	if room.areas[oid] != area {
		t.Fatal("unknown receipt lost reserved area")
	}
	room.members[1].lastAttack = time.Now().Add(-time.Second)
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("pending area reused for a second Apply")
	}
	resolved = true
	s.resolveCombatSettlements(context.Background(), repo)
	if room.areas[oid] != nil || mob.hp != 44661 || lives[0].session.Character.MP != 400 || calls != 1 {
		t.Fatal("exact receipt did not commit once")
	}
}

func TestMistEruptionKnownRollbackAndPreCommitAreaChange(t *testing.T) {
	for _, kind := range []string{"rollback", "expired", "replacement", "visit"} {
		t.Run(kind, func(t *testing.T) {
			s, lives, sinks, mob, oid, a := eruptionFixture(t)
			room := s.gameplay.rooms[mapKey{1, 40000}]
			original := room.areas[oid]
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return domain.CombatAttackResult{}, domain.ErrForbidden
			}}
			if kind != "rollback" {
				s.repo = &eruptionChangingRepo{change: func() {
					s.gameplay.mu.Lock()
					defer s.gameplay.mu.Unlock()
					switch kind {
					case "expired":
						original.expiresAt = time.Now().Add(-time.Second)
					case "replacement":
						replacement := *original
						replacement.reservation = nil
						room.areas[oid] = &replacement
					case "visit":
						replacement := *room.members[1]
						room.members[1] = &replacement
					}
				}, apply: func() { calls++ }}
			}
			if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			if kind == "rollback" {
				wantCalls = 1
			}
			if calls != wantCalls || mob.hp != 55000 || lives[0].session.Character.MP != 500 || original.reservation != nil || room.areas[oid] == nil || sinks[1].count(protocol.SendAffectedAreaRemoved) != 0 {
				t.Fatal("rollback/changed area mutated damage or retained claim")
			}
		})
	}
}

type eruptionChangingRepo struct {
	domain.Repository
	change func()
	apply  func()
}

func (r *eruptionChangingRepo) CombatParties(context.Context, domain.WorldLease, []int64) ([]domain.CombatPartyPin, error) {
	r.change()
	return nil, nil
}
func (r *eruptionChangingRepo) ApplyCombatAttack(context.Context, domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
	r.apply()
	return domain.CombatAttackResult{}, domain.ErrForbidden
}
func (*eruptionChangingRepo) CombatAttackOutcome(context.Context, domain.WorldLease, string, string) (*domain.CombatAttackResult, error) {
	return nil, nil
}

func TestMistEruptionCommittedMissConsumesOnlyNamedMist(t *testing.T) {
	s, lives, sinks, mob, oid, a := eruptionFixture(t)
	for i := range a.Targets[0].Lines {
		a.Targets[0].Lines[i] = protocol.AttackLine{}
	}
	// The selected area is detonated by the original client even if every
	// selected monster line misses. It must not remain usable for another cast.
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		return eruptionResult(q), nil
	}}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || mob.hp != 55000 || lives[0].session.Character.MP != 400 || s.gameplay.rooms[mapKey{1, 40000}].areas[oid] != nil {
		t.Fatal("committed miss did not consume area/cost once")
	}
}

func TestMistEruptionZeroTargetsStillConsumesSelectedAreaAndCost(t *testing.T) {
	s, lives, sinks, mob, oid, a := eruptionFixture(t)
	a.Targets = nil
	a.TByte = 5
	// Header28 + actorXY/flag + count/list; remove only the target record.
	a.Wire = append(append([]byte(nil), a.Wire[:28]...), a.Wire[len(a.Wire)-10:]...)
	a.Wire[1] = 5
	if parsed, err := protocol.ReadAttack(protocol.RecvMagicAttack, protocol.NewReader(a.Wire)); err != nil || len(parsed.Targets) != 0 || len(parsed.EruptionAreas) != 1 {
		t.Fatalf("zero target fixture: %+v %v", parsed, err)
	}
	calls := 0
	s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		calls++
		if len(q.Targets) != 0 || q.LinesPerTarget != 5 || q.Cost.MP != 100 || q.Cost.CooldownMS != 10000 {
			t.Fatalf("zero target cost:%+v", q)
		}
		return eruptionResult(q), nil
	}}
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || mob.hp != 55000 || lives[0].session.Character.MP != 400 || len(lives[0].session.Character.SkillCooldowns) != 1 || s.gameplay.rooms[mapKey{1, 40000}].areas[oid] != nil || sinks[1].count(protocol.SendDamageMonster) != 0 || sinks[0].count(0x19f) != 1 || sinks[1].count(0x19f) != 0 {
		t.Fatalf("zero target state calls%d hp%d mp%d area%v damageframes%d", calls, mob.hp, lives[0].session.Character.MP, s.gameplay.rooms[mapKey{1, 40000}].areas[oid], sinks[1].count(protocol.SendDamageMonster))
	}
}

func TestMistEruptionUnknownOldVisitDoesNotRemoveNewMist(t *testing.T) {
	s, lives, sinks, _, oid, a := eruptionFixture(t)
	var result domain.CombatAttackResult
	resolved := false
	repo := &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		result = eruptionResult(q)
		return domain.CombatAttackResult{}, errors.New("lost")
	}, outcome: func(string, string) (*domain.CombatAttackResult, error) {
		if !resolved {
			return nil, errors.New("unknown")
		}
		return &result, nil
	}}
	s.repo = repo
	if err := s.genericAttackGameplay(context.Background(), 1, lives[0], sinks[0], a); err != nil {
		t.Fatal(err)
	}
	room := s.gameplay.rooms[mapKey{1, 40000}]
	old := room.areas[oid]
	entry := *room.members[1]
	room.members[1] = &entry
	newID, err := s.SpawnFieldObject(1, lives[0], 2111003, 20, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resolved = true
	s.resolveCombatSettlements(context.Background(), repo)
	if room.areas[newID] == nil || room.areas[oid] != old || old.reservation != nil || lives[0].session.Character.MP != 500 || sinks[0].count(protocol.SendAffectedAreaRemoved) != 0 || sinks[1].count(protocol.SendAffectedAreaRemoved) != 0 {
		t.Fatal("old receipt crossed visit or removed fresh area")
	}
}

func TestMistEruptionNativeRectangleUsesAuthoritativeActor(t *testing.T) {
	for _, v := range []struct {
		name   string
		dx, dy int
		want   bool
	}{
		{"left_top", -500, -350, true}, {"inside_bottom_right", 499, 349, true},
		{"left_outside", -501, 0, false}, {"top_outside", 0, -351, false},
		{"right_edge", 500, 0, false}, {"bottom_edge", 0, 350, false},
	} {
		t.Run(v.name, func(t *testing.T) {
			s, lives, _, _, oid, a := eruptionFixture(t)
			c := lives[0].session.Character
			_, effect, _, ok := resolveAttackEffect(c, &a, time.Now())
			if !ok {
				t.Fatal("actual WZ effect unavailable")
			}
			room := s.gameplay.rooms[mapKey{1, 40000}]
			room.areas[oid].x, room.areas[oid].y = c.X+v.dx, c.Y+v.dy
			// Client coordinates cannot translate the server's selection rectangle.
			a.X, a.Y = 30000, 30000
			_, err := ownedEruptionAreasLocked(room, room.members[1], lives[0].session, a, effect, time.Now())
			if (err == nil) != v.want {
				t.Fatalf("delta(%d,%d) err%v", v.dx, v.dy, err)
			}
		})
	}
}
