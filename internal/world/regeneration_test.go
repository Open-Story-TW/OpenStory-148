package world

import (
	"context"
	"errors"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func recoveryWorldFixture(t *testing.T, sink packetSink) (*Server, *liveCharacter, time.Time) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"Item.wz", "Character.wz/Weapon", "Map.wz/Map/Map0"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	xml := `<imgdir name="ItemOption.img">`
	for _, opt := range []struct {
		id    int
		key   string
		value int
	}{{10151, "RecoveryHP", 2}, {10156, "RecoveryMP", 3}} {
		xml += fmt.Sprintf(`<imgdir name="%06d"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="%s" value="%d"/></imgdir></imgdir></imgdir>`, opt.id, opt.key, opt.value)
	}
	xml += `</imgdir>`
	for path, data := range map[string]string{"Item.wz/ItemOption.img.xml": xml, "Character.wz/Weapon/01302000.img.xml": `<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`, "Map.wz/Map/Map0/000040000.img.xml": `<imgdir name="map"><imgdir name="info"/><imgdir name="foothold"><imgdir name="0"><imgdir name="0"><imgdir name="1"><int name="x1" value="-500"/><int name="y1" value="0"/><int name="x2" value="500"/><int name="y2" value="0"/></imgdir></imgdir></imgdir></imgdir></imgdir>`} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	now := time.Now()
	c := domain.Character{ID: 1, AccountID: 1, WorldID: 1, MapID: 40000, Job: 200, Level: 30, HP: 20, MP: 20, MaxHP: 100, MaxMP: 100, BaseMaxHP: 100, BaseMaxMP: 100, VitalsModel: 1, INT: 500, Revision: 1, Equipment: []domain.Equipment{{ID: 10, ItemID: 1302000, Slot: -11, Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10151, 10151, 10156, 10156, 10156}}}}}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, AccountID: 1, ChannelID: 1, SessionID: "regen-session", Generation: 1, Lease: domain.WorldLease{WorldID: 1, InstanceID: "recovery", Epoch: 1}, Character: c}}
	s := &Server{skills: newSkillSpace(), lease: live.session.Lease}
	seedBoundScene(t, s, live, sink)
	if err := s.enterSkills(context.Background(), 1, live, sink); err != nil {
		t.Fatal(err)
	}
	ground, err := content.RecoveryGroundSupport(root, c.MapID)
	if err != nil {
		t.Fatal(err)
	}
	move := protocol.PlayerMove{Pose: protocol.PlayerMovementPose{Known: true, State: 4}}
	visit := s.sceneFor(live, sink)
	s.observeRecoveryMovement(visit, live.session, move, ground, now)
	s.observeRecoveryMovement(visit, live.session, move, ground, now)
	s.skills.members[1].lastRegen = now
	return s, live, now
}
func recoveryCommand(op uint16, tick uint32, hp, mp int) protocol.Regeneration {
	w := protocol.NewRawWriter()
	w.U32(tick)
	w.U32(0x1400)
	w.U32(0)
	w.I16(int16(hp))
	w.I16(int16(mp))
	if op == protocol.RecvRegeneration {
		w.U8(0)
	}
	q, err := protocol.ReadRegenerationOpcode(op, protocol.NewReader(w.Packet()))
	if err != nil {
		panic(err)
	}
	return q
}
func TestRecoverySourcesSeparateComponentsTicksCadenceAndNoCatchup(t *testing.T) {
	sink := &gameplayTestSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	run := func(op uint16, tick uint32, hp, mp int, offset time.Duration) {
		t.Helper()
		if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(op, tick, hp, mp), start.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	run(protocol.RecvPotentialRegeneration, 100, 99, 0, 4*time.Second-time.Millisecond)
	if live.session.Character.HP != 20 || s.skills.members[1].regeneration[1][0].seen {
		t.Fatal("early event consumed budget")
	}
	run(protocol.RecvPotentialRegeneration, 100, 99, 0, 4*time.Second)
	run(protocol.RecvPotentialRegeneration, 100, 0, 99, 4*time.Second)
	if c := live.session.Character; c.HP != 24 || c.MP != 29 {
		t.Fatal("same tick split components lost", c.HP, c.MP)
	}
	run(protocol.RecvPotentialRegeneration, 100, 0, 99, 8*time.Second)
	run(protocol.RecvPotentialRegeneration, 101, 0, 99, 8*time.Second-time.Millisecond)
	if live.session.Character.MP != 29 {
		t.Fatal("replay or early event healed")
	}
	run(protocol.RecvPotentialRegeneration, 101, 0, 99, 8*time.Second)
	run(protocol.RecvRegeneration, 100, 99, 0, 10*time.Second)
	run(protocol.RecvRegeneration, 100, 0, 99, 10*time.Second)
	if c := live.session.Character; c.HP != 34 || c.MP != 41 {
		t.Fatal("natural source mixed counter or guessed INT heal", c.HP, c.MP)
	}
	run(protocol.RecvRegeneration, 101, 99, 0, 10*time.Minute)
	if live.session.Character.HP != 44 {
		t.Fatal("server caught up missed timer periods")
	}
	if !live.dirty || s.online[1].Character.HP != 44 {
		t.Fatal("checkpoint/online projection lost recovery")
	}
	// A later old native tick cannot spend a new time budget.
	run(protocol.RecvRegeneration, 99, 99, 0, 11*time.Minute)
	if live.session.Character.HP != 44 {
		t.Fatal("old tick replay healed after rate elapsed")
	}
}
func TestRecoveryExpiryDeadFuryCapsAndPendingParty(t *testing.T) {
	for _, kind := range []string{"expired", "bag", "hidden", "dead", "fury", "full", "pending"} {
		t.Run(kind, func(t *testing.T) {
			sink := &gameplayTestSink{}
			s, live, start := recoveryWorldFixture(t, sink)
			m := s.skills.members[1]
			c := &live.session.Character
			switch kind {
			case "expired":
				c.Equipment[0].ExpiresAt = start.UnixMilli()
			case "bag":
				c.Equipment[0].Slot = 1
			case "hidden":
				c.Equipment[0].Potential.RawState = 1
			case "dead":
				c.HP = 0
			case "fury":
				c.Job = 3112
			case "full":
				c.HP, c.MP = 100, 100
			case "pending":
				s.skills.partyPending = map[string]*partySkillTicket{"unknown": {peers: []partySkillPeer{{member: m, session: live.session}}}}
			}
			hp, mp := c.HP, c.MP
			if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99), start.Add(5*time.Second)); err != nil {
				t.Fatal(err)
			}
			if kind == "fury" {
				if c.HP != hp+4 || c.MP != mp {
					t.Fatal("Demon HP/MP wrong", c.HP, c.MP)
				}
			} else if c.HP != hp || c.MP != mp || m.regeneration[1][0].seen || m.regeneration[1][1].seen || live.dirty {
				t.Fatal("ineligible request mutated vitals/budget", kind, c.HP, c.MP)
			}
			if kind == "pending" {
				delete(s.skills.partyPending, "unknown")
				if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99), start.Add(5*time.Second)); err != nil {
					t.Fatal(err)
				}
				if c.HP != hp+4 || c.MP != mp+9 {
					t.Fatal("resolved same tick could not run")
				}
			}
		})
	}
	sink := &gameplayTestSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	c := &live.session.Character
	c.HP, c.MP = 150, 99
	c.MaxHP = 160
	c.SkillBuffs = []domain.SkillBuff{{ID: 1301007, Stat: "MAXHP", Value: 60, ExpiresAt: start.Add(time.Second)}}
	if err := s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99), start.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if c.HP != 100 || c.MaxHP != 100 || c.MP != 100 || c.BaseMaxHP != 100 {
		t.Fatal("expired maximum used as recovery headroom", c.HP, c.MP, c.MaxHP)
	}
}
func waitRecoveryPersistenceHeld(t *testing.T, live *liveCharacter) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !live.persistMu.TryLock() {
			return
		}
		live.persistMu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("recovery never reached persistence boundary")
}
func TestRecoveryCannotOutliveSameMapVisitOrGeneration(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			sink := &gameplayTestSink{}
			s, live, start := recoveryWorldFixture(t, sink)
			old := s.sceneFor(live, sink)
			release, err := s.lockScenePublication(1)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99), start.Add(5*time.Second))
			}()
			waitRecoveryPersistenceHeld(t, live)
			session := live.session
			session.Character.HP = 7
			if replace {
				session.Generation++
			}
			next := makeSceneVisit(session, live, sink)
			s.installScene(next, session)
			release()
			if err := <-done; !errors.Is(err, domain.ErrStaleOwner) {
				t.Fatal("old recovery did not reject", err)
			}
			if live.session.Character.HP != 7 || len(sink.packets) != 0 || old == s.sceneFor(live, sink) || s.skills.members[1].regeneration[1][0].seen {
				t.Fatal("late recovery crossed visit")
			}
		})
	}
}

type recoveryFailureSink struct{ calls int }

func (s *recoveryFailureSink) WritePacket([]byte) error {
	s.calls++
	return errors.New("lost response")
}
func TestRecoveryLostReplyAndConcurrentDuplicateNeverReapply(t *testing.T) {
	sink := &recoveryFailureSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	q := recoveryCommand(protocol.RecvPotentialRegeneration, 1, 99, 99)
	if err := s.regenerateAt(context.Background(), live, sink, q, start.Add(5*time.Second)); err == nil {
		t.Fatal("write failure hidden")
	}
	if err := s.regenerateAt(context.Background(), live, sink, q, start.Add(10*time.Second)); err == nil {
		t.Fatal("write failure hidden")
	}
	if c := live.session.Character; c.HP != 24 || c.MP != 29 || !live.dirty {
		t.Fatal("lost response reran recovery")
	}
	normal := &gameplayTestSink{}
	s, live, start = recoveryWorldFixture(t, normal)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.regenerateAt(context.Background(), live, normal, q, start.Add(5*time.Second))
		}()
	}
	wg.Wait()
	if live.session.Character.HP != 24 || live.session.Character.MP != 29 {
		t.Fatal("concurrent duplicate applied twice")
	}
}
func TestRecoveryPublicationRetainsSceneGateWithoutRegistryLocks(t *testing.T) {
	sink := &sceneBlockingSink{entered: make(chan struct{}), release: make(chan struct{})}
	s, live, start := recoveryWorldFixture(t, sink)
	done := make(chan error, 1)
	go func() {
		done <- s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 1, 9, 9), start.Add(5*time.Second))
	}()
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("self packet not published")
	}
	if !s.skills.mu.TryLock() {
		t.Fatal("skill registry held over write")
	}
	s.skills.mu.Unlock()
	if !s.mu.TryLock() {
		t.Fatal("online registry held over write")
	}
	s.mu.Unlock()
	acquired := make(chan struct{})
	go func() {
		unlock, err := s.lockScenePublication(1)
		if err == nil {
			close(acquired)
			unlock()
		}
	}()
	select {
	case <-acquired:
		t.Fatal("new scene overtook old HPMP publication")
	case <-time.After(20 * time.Millisecond):
	}
	close(sink.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("publication gate leaked")
	}
}
func TestRecoveryTickWrapIsMonotonicPerComponent(t *testing.T) {
	now := time.Now()
	c := regenerationCounter{at: now, tick: 0xfffffff0, seen: true}
	if !c.eligible(0x10, now.Add(4*time.Second), now, 4*time.Second) || c.eligible(0xffffffe0, now.Add(5*time.Second), now, 4*time.Second) || c.eligible(0xfffffff0, now.Add(5*time.Second), now, 4*time.Second) {
		t.Fatal("native tick wrap/replay ordering lost")
	}
}
func TestRecoveryNativeDispatchUsesExactBodyAndMode(t *testing.T) {
	sink := &gameplayTestSink{}
	s, live, start := recoveryWorldFixture(t, sink)
	s.skills.members[1].lastRegen = start.Add(-20 * time.Second)
	s.skills.members[1].regeneration = [2][2]regenerationCounter{}
	for _, op := range []uint16{protocol.RecvRegeneration, protocol.RecvPotentialRegeneration} {
		q := recoveryCommand(op, 99, 99, 99)
		handled, err := s.handleSkills(context.Background(), live, sink, op, protocol.NewReader(q.Wire))
		if !handled || err != nil {
			t.Fatal("native body lost at dispatch", op, handled, err)
		}
	}
	if c := live.session.Character; c.HP != 34 || c.MP != 32 {
		t.Fatal("dispatch caps/source wrong", c.HP, c.MP)
	}
	before := live.session.Character
	q := recoveryCommand(protocol.RecvRegeneration, 100, 99, 99)
	q.Wire[len(q.Wire)-1] = 2
	if handled, err := s.handleSkills(context.Background(), live, sink, q.Opcode, protocol.NewReader(q.Wire)); !handled || err != nil || live.session.Character.HP != before.HP || live.session.Character.MP != before.MP {
		t.Fatal("unknown chair state mutated")
	}
	bad := append([]byte(nil), q.Wire...)
	bad[5] = 0
	if handled, err := s.handleSkills(context.Background(), live, sink, q.Opcode, protocol.NewReader(bad)); !handled || err != nil || live.session.Character.HP != before.HP {
		t.Fatal("malformed regen did not safely resync")
	}
}
