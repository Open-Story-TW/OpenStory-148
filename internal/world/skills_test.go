package world

import (
	"context"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
	"time"
)

type skillTestRepository struct {
	domain.Repository
	cast   func(domain.SkillCastRequest) (domain.SkillResult, error)
	cancel func(domain.CharacterSession, int) (domain.SkillResult, error)
}

func (r skillTestRepository) AllocateSkill(context.Context, domain.SkillAllocateRequest) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func (r skillTestRepository) CastSkill(_ context.Context, q domain.SkillCastRequest) (domain.SkillResult, error) {
	return r.cast(q)
}
func (r skillTestRepository) CancelSkill(_ context.Context, q domain.CharacterSession, id int) (domain.SkillResult, error) {
	return r.cancel(q, id)
}
func (r skillTestRepository) TickSkillBuffs(context.Context, domain.CharacterSession) (domain.SkillResult, error) {
	return domain.SkillResult{}, domain.ErrForbidden
}
func TestSkillBuffTwoPlayerPublishAndLateJoin(t *testing.T) {
	s := &Server{skills: newSkillSpace(), online: map[int64]domain.CharacterSession{}}
	a := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, Level: 2, HP: 50, MP: 5, MaxHP: 50, MaxMP: 5, Revision: 1}}}
	b := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000, HP: 50}}}
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	if e := s.enterSkills(context.Background(), 1, a, first); e != nil {
		t.Fatal(e)
	}
	if e := s.enterSkills(context.Background(), 1, b, second); e != nil {
		t.Fatal(e)
	}
	s.repo = skillTestRepository{cast: func(q domain.SkillCastRequest) (domain.SkillResult, error) {
		c := q.Session.Character
		c.MP = 1
		c.Revision++
		c.SkillBuffs = []domain.SkillBuff{{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: time.Now().Add(4 * time.Second)}}
		a.mu.Lock()
		a.session.Character.X = 80
		a.dirty = true
		a.mu.Unlock()
		return domain.SkillResult{Character: c, SkillID: 1002, Changed: true}, nil
	}, cancel: func(q domain.CharacterSession, id int) (domain.SkillResult, error) {
		c := q.Character
		old := c.SkillBuffs
		c.SkillBuffs = nil
		c.Revision++
		return domain.SkillResult{Character: c, Changed: true, Expired: old}, nil
	}}
	if e := s.castSkill(context.Background(), a, first, protocol.SkillCast{Tick: 1, SkillID: 1002, Level: 1}); e != nil {
		t.Fatal(e)
	}
	if a.session.Character.X != 80 || a.session.Character.MP != 1 || !a.dirty {
		t.Fatal("cast erased movement or cost")
	}
	if first.count(0x2d) != 1 || second.count(0x145) != 1 || second.count(0x144) != 1 {
		t.Fatal("missing local/foreign buff and effect")
	}
	late := &gameplayTestSink{}
	latePlayer := &liveCharacter{session: domain.CharacterSession{CharacterID: 3, ChannelID: 1, Character: domain.Character{ID: 3, MapID: 40000, HP: 50}}}
	if e := s.enterSkills(context.Background(), 1, latePlayer, late); e != nil {
		t.Fatal(e)
	}
	if late.count(0x145) != 1 {
		t.Fatal("late join missing activebuff")
	}
	if e := s.cancelSkill(context.Background(), a, first, 1002); e != nil {
		t.Fatal(e)
	}
	if first.count(0x2e) != 1 || second.count(0x146) != 1 || late.count(0x146) != 1 {
		t.Fatal("missing cancel synchronization")
	}
}

func TestIronBodyTwoPlayerPublishAndCancel(t *testing.T) {
	s := &Server{skills: newSkillSpace(), online: map[int64]domain.CharacterSession{}}
	a := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, Job: 100, Level: 10, HP: 50, MP: 30, MaxHP: 50, MaxMP: 30, Revision: 1}}}
	b := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000, HP: 50}}}
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	if err := s.enterSkills(context.Background(), 1, a, first); err != nil {
		t.Fatal(err)
	}
	if err := s.enterSkills(context.Background(), 1, b, second); err != nil {
		t.Fatal(err)
	}
	s.repo = skillTestRepository{cast: func(q domain.SkillCastRequest) (domain.SkillResult, error) {
		c := q.Session.Character
		c.MP -= 8
		c.Revision++
		c.SkillBuffs = []domain.SkillBuff{{ID: 1001003, Level: 1, Stat: "WDEF", Value: 10, ExpiresAt: time.Now().Add(110 * time.Second)}}
		return domain.SkillResult{Character: c, Changed: true}, nil
	}, cancel: func(q domain.CharacterSession, id int) (domain.SkillResult, error) {
		c := q.Character
		expired := c.SkillBuffs
		c.SkillBuffs = nil
		c.Revision++
		return domain.SkillResult{Character: c, Changed: true, Expired: expired}, nil
	}}
	if err := s.castSkill(context.Background(), a, first, protocol.SkillCast{Tick: 1, SkillID: 1001003, Level: 1}); err != nil {
		t.Fatal(err)
	}
	if a.session.Character.MP != 22 || first.count(0x2d) != 1 || second.count(0x145) != 1 || second.count(0x144) != 1 || first.count(0x19f) != 0 {
		t.Fatal("iron-body MP/foreign buff/effect/no-cooldown mismatch")
	}
	if err := s.cancelSkill(context.Background(), a, first, 1001003); err != nil {
		t.Fatal(err)
	}
	if len(a.session.Character.SkillBuffs) != 0 || first.count(0x2e) != 1 || second.count(0x146) != 1 {
		t.Fatal("iron-body cancellation not synchronized")
	}
}
func TestNativeDamageAndRegenAuthority(t *testing.T) {
	g, e := newGameplaySpace()
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{skills: newSkillSpace(), gameplay: g}
	a := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, Job: 0, HP: 50, MaxHP: 50, MaxMP: 5, Revision: 1}}}
	b := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000, HP: 50}}}
	first, second := &gameplayTestSink{}, &gameplayTestSink{}
	if e = s.enterGameplay(1, a, first); e != nil {
		t.Fatal(e)
	}
	_ = s.enterSkills(context.Background(), 1, a, first)
	_ = s.enterSkills(context.Background(), 1, b, second)
	var mob *gameplayMob
	for _, v := range g.rooms[mapKey{channel: 1, mapID: 40000}].mobs {
		mob = v
		break
	}
	if mob == nil {
		t.Fatal("missing actual WZ mob")
	}
	a.session.Character.X, a.session.Character.Y = mob.life.X, mob.life.Y
	q := protocol.PlayerDamage{Tick: 1, Type: -1, Damage: 1, MonsterID: mob.stats.ID, ObjectID: mob.objectID}
	wire := protocol.NewRawWriter()
	wire.U32(0)
	wire.U32(q.Tick)
	wire.U8(255)
	wire.U8(0)
	wire.I32(int32(q.Damage))
	wire.U16(0)
	wire.I32(int32(q.MonsterID))
	wire.I32(q.ObjectID)
	wire.U8(0)
	wire.I32(0)
	wire.I32(0)
	wire.U8(0)
	wire.U8(0)
	wire.U8(0) // 8893F8 appends the common flags byte after the mob branch.
	if handled, err := s.handleSkills(context.Background(), a, first, protocol.RecvTakeDamage, protocol.NewReader(wire.Packet())); !handled || err != nil {
		t.Fatalf("native contact packet not handled: %v %v", handled, err)
	}
	_ = s.takeDamage(context.Background(), a, first, q)
	if a.session.Character.HP != 49 || !a.dirty || second.count(0x13b) != 1 {
		t.Fatal("damage replay or foreign synchronization")
	}
	q.Tick++
	q.ObjectID++
	_ = s.takeDamage(context.Background(), a, first, q)
	if a.session.Character.HP != 49 {
		t.Fatal("unknown mob damaged player")
	}
	seedBoundScene(t, s, a, first)
	s.skills.members[1].recoveryPose = recoveryPose{visit: s.sceneFor(a, first), known: true, stable: true, grounded: true, x: a.session.Character.X, y: a.session.Character.Y, state: 4}
	a.session.Character.HP = 20
	s.skills.members[1].lastRegen = time.Now().Add(-11 * time.Second)
	_ = s.regenerate(context.Background(), a, first, protocol.Regeneration{Opcode: protocol.RecvRegeneration, Tick: 100, HP: 999, MP: 999})
	if a.session.Character.HP != 30 || a.session.Character.MP != 3 {
		t.Fatal("regen exceeded server cap")
	}
	_ = s.regenerate(context.Background(), a, first, protocol.Regeneration{Opcode: protocol.RecvRegeneration, Tick: 100, HP: 999, MP: 999})
	if a.session.Character.HP != 30 {
		t.Fatal("regen spam accepted")
	}
}
