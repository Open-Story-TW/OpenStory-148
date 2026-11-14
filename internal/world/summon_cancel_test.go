package world

import (
	"context"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func summonCancelFixture(t *testing.T) (*Server, []*liveCharacter, []*gameplayTestSink, int32, *int) {
	t.Helper()
	s, lives, sinks, _, oid := summonFixture(t)
	s.skills = newSkillSpace()
	lives[0].session.Character.SkillBuffs = []domain.SkillBuff{{ID: testSummonSkill, Stat: "SUMMON", Level: 2, Value: 1, ExpiresAt: time.Now().Add(time.Minute)}}
	for i, live := range lives {
		seedBoundScene(t, s, live, sinks[i])
		if err := s.enterSkills(context.Background(), 1, live, sinks[i]); err != nil {
			t.Fatal(err)
		}
	}
	calls := new(int)
	s.repo = skillTestRepository{cancel: func(q domain.CharacterSession, id int) (domain.SkillResult, error) {
		*calls++
		if q.CharacterID != 1 || id != testSummonSkill {
			t.Fatalf("cancelled wrong owner/source: %d/%d", q.CharacterID, id)
		}
		c := q.Character
		old := c.SkillBuffs
		c.SkillBuffs = nil
		c.Revision++
		return domain.SkillResult{Character: c, Expired: old, Changed: true}, nil
	}}
	return s, lives, sinks, oid, calls
}

func TestSummonCancellationNotifiesLocalOwnerAndPeer(t *testing.T) {
	for _, viaObject := range []bool{false, true} {
		t.Run(map[bool]string{false: "buff96", true: "object103"}[viaObject], func(t *testing.T) {
			s, lives, sinks, oid, calls := summonCancelFixture(t)
			var err error
			if viaObject {
				w := protocol.NewRawWriter()
				w.I32(oid)
				err = s.summonFieldPacket(context.Background(), 1, lives[0], sinks[0], protocol.RecvRemoveSummon, w.Packet())
			} else {
				err = s.cancelSkill(context.Background(), lives[0], sinks[0], testSummonSkill)
			}
			if err != nil || *calls != 1 || len(lives[0].session.Character.SkillBuffs) != 0 {
				t.Fatalf("durable cancel: calls=%d err=%v", *calls, err)
			}
			for i, sink := range sinks {
				if sink.count(protocol.SendRemoveSummon) != 1 {
					t.Fatalf("recipient%d missing exactly one removal", i)
				}
			}
			if sinks[0].count(0x2e) != 1 || sinks[1].count(0x146) != 1 {
				t.Fatal("missing local/foreign buff reset")
			}
			if s.summonCount(mapKey{1, 40000}, 1, testSummonSkill) != 0 {
				t.Fatal("removed buff retained room object")
			}
			// The old object cannot cancel a replacement, or drive more damage.
			newID, err := s.SpawnSummon(1, lives[0], testSummonSkill, 2, time.Now())
			if err != nil || newID == oid {
				t.Fatal("replacement", newID, err)
			}
			if err := s.cancelSummon(context.Background(), 1, lives[0], sinks[0], oid); err != nil {
				t.Fatal(err)
			}
			if *calls != 1 || s.summonCount(mapKey{1, 40000}, 1, testSummonSkill) != 1 {
				t.Fatal("stale object cancelled replacement")
			}
		})
	}
}

func TestSummonRemovalRejectsWrongOwnerVisitAndMalformedBody(t *testing.T) {
	for _, kind := range []string{"peer", "reentered", "expired", "malformed", "session"} {
		t.Run(kind, func(t *testing.T) {
			s, lives, sinks, oid, calls := summonCancelFixture(t)
			actor, sink := lives[0], sinks[0]
			w := protocol.NewRawWriter()
			w.I32(oid)
			switch kind {
			case "peer":
				actor, sink = lives[1], sinks[1]
			case "reentered":
				// Same CID/session/live, new immutable room entry must not inherit old summon.
				g := s.gameplay
				g.mu.Lock()
				room := g.rooms[mapKey{1, 40000}]
				copy := *room.members[1]
				room.members[1] = &copy
				g.mu.Unlock()
			case "expired":
				s.gameplay.mu.Lock()
				s.gameplay.rooms[mapKey{1, 40000}].summons[oid].expiresAt = time.Now().Add(-time.Second)
				s.gameplay.mu.Unlock()
			case "malformed":
				w.U8(0)
			case "session":
				actor.mu.Lock()
				actor.session.Generation++
				actor.mu.Unlock()
			}
			if err := s.summonFieldPacket(context.Background(), 1, actor, sink, protocol.RecvRemoveSummon, w.Packet()); err != nil {
				t.Fatal(err)
			}
			if *calls != 0 || sinks[0].count(protocol.SendRemoveSummon) != 0 || sinks[1].count(protocol.SendRemoveSummon) != 0 {
				t.Fatal("invalid removal reached repository or emitted removal")
			}
		})
	}
}

func TestSummonRemovalRespectsPendingCombat(t *testing.T) {
	s, lives, sinks, _, _, _ := pendingVitalsFixture(t, domain.CombatPhysicalSkill)
	oid, err := s.SpawnSummon(1, lives[0], testSummonSkill, 2, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.repo = skillTestRepository{cancel: func(domain.CharacterSession, int) (domain.SkillResult, error) {
		calls++
		return domain.SkillResult{}, nil
	}}
	if err := s.cancelSummon(context.Background(), 1, lives[0], sinks[0], oid); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || s.summonCount(mapKey{1, 40000}, 1, testSummonSkill) != 1 {
		t.Fatal("pending receipt lost summon")
	}
}

func TestSummonRemovalSpecialSkillsAndStoreFailure(t *testing.T) {
	for _, id := range []int{35111002, 35121010, 35121011} {
		t.Run(strconv.Itoa(id), func(t *testing.T) {
			s, lives, sinks, oid, calls := summonCancelFixture(t)
			s.gameplay.mu.Lock()
			s.gameplay.rooms[mapKey{1, 40000}].summons[oid].skillID = id
			s.gameplay.mu.Unlock()
			if err := s.cancelSummon(context.Background(), 1, lives[0], sinks[0], oid); err != nil {
				t.Fatal(err)
			}
			want := 1
			if id == 35121011 {
				want = 0
			}
			if *calls != 0 || s.summonCount(mapKey{1, 40000}, 1, testSummonSkill) != want || len(lives[0].session.Character.SkillBuffs) != 1 {
				t.Fatal("special removal changed the durable buff or ignored protected object")
			}
			if id == 35121011 && (sinks[0].count(protocol.SendRemoveSummon) != 1 || sinks[1].count(protocol.SendRemoveSummon) != 1) {
				t.Fatal("healing robot removal must reach both views")
			}
		})
	}
	t.Run("store_failure", func(t *testing.T) {
		s, lives, sinks, oid, _ := summonCancelFixture(t)
		s.repo = skillTestRepository{cancel: func(domain.CharacterSession, int) (domain.SkillResult, error) {
			return domain.SkillResult{}, domain.ErrForbidden
		}}
		if err := s.cancelSummon(context.Background(), 1, lives[0], sinks[0], oid); err != nil {
			t.Fatal(err)
		}
		if s.summonCount(mapKey{1, 40000}, 1, testSummonSkill) != 1 || len(lives[0].session.Character.SkillBuffs) != 1 || sinks[0].count(protocol.SendRemoveSummon) != 0 || sinks[1].count(protocol.SendRemoveSummon) != 0 {
			t.Fatal("failed store cancellation silently removed live summon")
		}
	})
}

func TestRemoveSummonsSessionFilterAndOwnerLeave(t *testing.T) {
	s, lives, sinks, _, _ := summonCancelFixture(t)
	stale := lives[0].session
	stale.Generation--
	s.RemoveSummons(1, 40000, 1, testSummonSkill, stale)
	if s.summonCount(mapKey{1, 40000}, 1, testSummonSkill) != 1 || sinks[0].count(protocol.SendRemoveSummon) != 0 {
		t.Fatal("stale session cancelled summon")
	}
	s.leaveGameplay(1, 40000, 1, sinks[0])
	if sinks[0].count(protocol.SendRemoveSummon) != 0 || sinks[1].count(protocol.SendRemoveSummon) != 1 {
		t.Fatal("owner leave must remove for remaining peer only")
	}
}

func TestNativeElquinesAttackFrameRoutesToOwnedSummon(t *testing.T) {
	// Actual CID4904 2026-11-07T00:05:33 frame, before cancellation:
	// source2121005, oid1000036, target1000033, raw7786. Only fixture object IDs
	// and server-side cap differ; no synthetic wire layout is substituted.
	body, err := hex.DecodeString("64420f00ce38861e840106054b024c054b020000000061420f0005870100070005010c054b020b054b0238046a1e000081b29958")
	if err != nil {
		t.Fatal(err)
	}
	body = protocol.UpgradeV145SummonAttackBody(body) // archived v145 frame
	in, err := protocol.ReadSummonAttack(protocol.NewReader(body))
	if err != nil || in.ObjectID != 1000036 || len(in.Targets) != 1 || in.Targets[0].ObjectID != 1000033 || in.Targets[0].Lines[0].Raw != 7786 {
		t.Fatalf("actual frame: %+v %v", in, err)
	}
	s, lives, sinks, mob, oid := summonFixture(t)
	mob.hp = 1000
	in.ObjectID = oid
	in.Targets[0].ObjectID = mob.objectID
	s.repo = &sharedCombatRepo{apply: func(domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		t.Fatal("nonlethal summon unexpectedly charged cost")
		return domain.CombatAttackResult{}, nil
	}}
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	if mob.hp >= 1000 || sinks[1].count(protocol.SendSummonAttack) != 1 {
		t.Fatal("native summon shape did not apply damage and broadcast")
	}
	before := mob.hp
	s.RemoveSummons(1, 40000, 1, testSummonSkill, lives[0].session)
	in.Tick++
	if err := s.summonAttackGameplay(context.Background(), 1, lives[0], sinks[0], in); err != nil {
		t.Fatal(err)
	}
	if mob.hp != before {
		t.Fatal("cancelled object still dealt damage")
	}
}
