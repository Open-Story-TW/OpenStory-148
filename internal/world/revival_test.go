package world

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type revivalSkillRepo struct {
	gameplayTestRepo
	domain.SkillRepository
	tick func(domain.CharacterSession) (domain.SkillResult, error)
}

func (r revivalSkillRepo) TickSkillBuffs(_ context.Context, s domain.CharacterSession) (domain.SkillResult, error) {
	return r.tick(s)
}

func TestOrdinaryRevivalPersistsTrustedMapAndBoundedHP(t *testing.T) {
	for _, maxHP := range []int{50, 2500} {
		t.Run(strconv.Itoa(maxHP), func(t *testing.T) {
			g, err := newGameplaySpace()
			if err != nil {
				t.Fatal(err)
			}
			var saved []domain.Character
			sink := &gameplayTestSink{}
			s := &Server{gameplay: g, maps: newMapSpace(800), drops: newDropSpace(), online: make(map[int64]domain.CharacterSession), repo: gameplayTestRepo{save: func(_ domain.CharacterSession, c domain.Character) error {
				if sink.count(protocol.SendSetField) != 0 {
					t.Fatal("map sent before durable revival")
				}
				saved = append(saved, c)
				return nil
			}}}
			p := &liveCharacter{dirty: true, session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, HP: 0, MaxHP: maxHP, MP: 7, MaxMP: 10, Revision: 1}}}
			seedBoundScene(t, s, p, sink)
			if err = s.enterGameplay(1, p, sink); err != nil {
				t.Fatal(err)
			}
			q := protocol.PortalRequest{TargetMap: 0, Revival: &protocol.RevivalRequest{}}
			if err = s.portalGameplay(context.Background(), 1, p, sink, q); err != nil {
				t.Fatal(err)
			}
			if len(saved) != 1 || saved[0].MapID != 40000 || saved[0].HP != min(maxHP, 1500) || saved[0].MP != 7 || saved[0].SpawnPoint != 0 {
				t.Fatalf("untrusted or non-atomic revival: %+v", saved)
			}
			if p.session.Character.Revision != 2 || p.dirty || sink.count(protocol.SendSetField) != 1 {
				t.Fatal("missing committed map transition")
			}
			if err = s.portalGameplay(context.Background(), 1, p, sink, q); err != nil {
				t.Fatal(err)
			}
			if len(saved) != 1 || sink.count(protocol.SendSetField) != 1 {
				t.Fatal("duplicate return revived a living character")
			}
		})
	}
}

func TestOrdinaryRevivalRejectsPaidAndRollsBackFailedSave(t *testing.T) {
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	var attempts int
	s := &Server{gameplay: g, repo: gameplayTestRepo{save: func(_ domain.CharacterSession, _ domain.Character) error {
		attempts++
		return errors.New("database unavailable")
	}}}
	p := &liveCharacter{dirty: true, session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, HP: 0, MaxHP: 50, MP: 3, Revision: 4}}}
	sink := &gameplayTestSink{}
	seedBoundScene(t, s, p, sink)
	for _, q := range []protocol.PortalRequest{
		{TargetMap: 0, Revival: &protocol.RevivalRequest{WheelType: 1}},
		{TargetMap: 104000000, Revival: &protocol.RevivalRequest{}},
	} {
		if err = s.portalGameplay(context.Background(), 1, p, sink, q); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != 0 {
		t.Fatal("paid or client-selected revival reached storage")
	}
	if err = s.portalGameplay(context.Background(), 1, p, sink, protocol.PortalRequest{TargetMap: 0, Revival: &protocol.RevivalRequest{}}); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || p.session.Character.HP != 0 || p.session.Character.Revision != 4 || !p.dirty || sink.count(protocol.SendSetField) != 0 {
		t.Fatal("failed save changed dead character or room")
	}
	// The persistMu-protected check also rejects a character healed or moved
	// after the initial request snapshot, before any map/storage mutation.
	current, _ := g.catalog.Map(40000)
	entry, _ := current.Spawn()
	p.session.Character.HP = 1
	if err = s.warpGameplayTransition(context.Background(), 1, p, sink, 40000, entry, &revivalTransition{fromMap: 40000}); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatal("revival did not recheck authoritative death")
	}
}

func TestRevivalCannotRaceDeathBuffCancellation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			g, err := newGameplaySpace()
			if err != nil {
				t.Fatal(err)
			}
			var ticks, saves int
			buff := domain.SkillBuff{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: time.Now().Add(time.Minute)}
			p := &liveCharacter{dirty: true, session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, MaxHP: 50, HP: 0, MP: 3, Revision: 1, SkillBuffs: []domain.SkillBuff{buff}}}}
			sink, peerSink := &gameplayTestSink{}, &gameplayTestSink{}
			s := &Server{gameplay: g, skills: newSkillSpace(), maps: newMapSpace(800), drops: newDropSpace(), online: make(map[int64]domain.CharacterSession)}
			peer := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000, Level: 2, HP: 50}}}
			s.maps.Enter(1, 40000, p.session.Character, sink)
			s.maps.Enter(1, 40000, peer.session.Character, peerSink)
			if err = s.enterSkills(context.Background(), 1, p, sink); err != nil {
				t.Fatal(err)
			}
			if err = s.enterSkills(context.Background(), 1, peer, peerSink); err != nil {
				t.Fatal(err)
			}
			s.repo = revivalSkillRepo{gameplayTestRepo: gameplayTestRepo{save: func(_ domain.CharacterSession, c domain.Character) error {
				saves++
				if ticks != 1 || c.Revision != 2 || len(c.SkillBuffs) != 0 || c.HP != 50 {
					t.Fatalf("return raced cancellation: %+v", c)
				}
				return nil
			}}, tick: func(session domain.CharacterSession) (domain.SkillResult, error) {
				ticks++
				if session.Character.HP != 0 {
					t.Fatal("death cancellation saw restored HP")
				}
				if fail {
					return domain.SkillResult{}, errors.New("database unavailable")
				}
				c := session.Character
				c.SkillBuffs = nil
				c.Revision++
				return domain.SkillResult{Character: c, Changed: true, Expired: []domain.SkillBuff{buff}}, nil
			}}
			seedBoundScene(t, s, p, sink)
			if err = s.enterGameplay(1, p, sink); err != nil {
				t.Fatal(err)
			}
			if err = s.portalGameplay(context.Background(), 1, p, sink, protocol.PortalRequest{TargetMap: 0, Revival: &protocol.RevivalRequest{}}); err != nil {
				t.Fatal(err)
			}
			if fail {
				if saves != 0 || p.session.Character.HP != 0 || len(p.session.Character.SkillBuffs) != 1 || sink.count(protocol.SendSetField) != 0 {
					t.Fatal("revived before failed cancellation committed")
				}
			} else {
				if saves != 1 || p.session.Character.Revision != 3 || sink.count(0x2e) != 1 || peerSink.count(0x146) != 1 {
					t.Fatal("death buff cancellation not published to both actors")
				}
			}
		})
	}
}

func TestLethalNativeHitImmediatelyCancelsBuffForBothPlayers(t *testing.T) {
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	buff := domain.SkillBuff{ID: 1001, Level: 1, Stat: "RECOVERY", Value: 4, ExpiresAt: time.Now().Add(time.Minute), NextTick: time.Now().Add(time.Second)}
	a := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, HP: 1, MaxHP: 50, Revision: 1, SkillBuffs: []domain.SkillBuff{buff}}}}
	b := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000, HP: 50}}}
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	s := &Server{gameplay: g, skills: newSkillSpace(), online: map[int64]domain.CharacterSession{}}
	var ticks int
	s.repo = revivalSkillRepo{tick: func(session domain.CharacterSession) (domain.SkillResult, error) {
		ticks++
		if session.Character.HP != 0 {
			t.Fatal("lethal hit not carried into cancellation")
		}
		c := session.Character
		c.SkillBuffs = nil
		c.Revision++
		return domain.SkillResult{Character: c, Changed: true, Expired: []domain.SkillBuff{buff}}, nil
	}}
	if err = s.enterGameplay(1, a, first); err != nil {
		t.Fatal(err)
	}
	if err = s.enterSkills(context.Background(), 1, a, first); err != nil {
		t.Fatal(err)
	}
	if err = s.enterSkills(context.Background(), 1, b, second); err != nil {
		t.Fatal(err)
	}
	var mob *gameplayMob
	for _, m := range g.rooms[mapKey{channel: 1, mapID: 40000}].mobs {
		mob = m
		break
	}
	if mob == nil {
		t.Fatal("missing real starter mob")
	}
	a.session.Character.X, a.session.Character.Y = mob.life.X, mob.life.Y
	if err = s.takeDamage(context.Background(), a, first, protocol.PlayerDamage{Tick: 1, Type: -1, Damage: 1, MonsterID: mob.stats.ID, ObjectID: mob.objectID}); err != nil {
		t.Fatal(err)
	}
	if ticks != 1 || a.session.Character.HP != 0 || len(a.session.Character.SkillBuffs) != 0 || first.count(0x2e) != 1 || second.count(0x146) != 1 {
		t.Fatal("lethal hit left active recovery or missed multiplayer cancellation")
	}
}
